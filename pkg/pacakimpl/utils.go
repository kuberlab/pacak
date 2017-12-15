package pacakimpl

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	git "github.com/gogits/git-module"
	"github.com/kuberlab/pacak/pkg/errors"
	"github.com/kuberlab/pacak/pkg/process"
	"github.com/kuberlab/pacak/pkg/sync"
	"github.com/kuberlab/pacak/pkg/util"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"
)

var pullTimeout time.Duration
var cloneTimeout time.Duration
var repoWorkingPool = sync.NewExclusivePool()

func init() {
	pullTimeout = time.Minute
	cloneTimeout = time.Minute
	for configKey, defaultValue := range map[string]string{"user.name": "pacak", "user.email": "pacak@kuberlab.com"} {
		if stdout, stderr, err := process.Exec("Git Settings(get "+configKey+")", "git", "config", "--get", configKey); err != nil || strings.TrimSpace(stdout) == "" {
			// ExitError indicates this config is not set
			if _, ok := err.(*exec.ExitError); ok || strings.TrimSpace(stdout) == "" {
				if _, stderr, gerr := process.Exec("Git Settings(set "+configKey+")", "git", "config", "--global", configKey, defaultValue); gerr != nil {
					logrus.Fatalf("Fail to set git %s(%s): %s", configKey, gerr, stderr)
				}
				logrus.Infof("Git config %s set to %s", configKey, defaultValue)
			} else {
				logrus.Fatalf("Fail to get git %s(%s): %s", configKey, err, stderr)
			}
		}
	}
}

type GitInterface interface {
	InitRepository(committer git.Signature, repo string, files []GitFile) error
	GetRepository(repo string) (PacakRepo, error)
}

type PacakRepo interface {
	Save(committer git.Signature, message string, oldBrach, newBranch string, files []GitFile) (string, error)
}

type pacakRepo struct {
	r         *git.Repository
	localPath string
}

type gitInterface struct {
	gitRoot   string
	localRoot string
}

func NewGitInterface(gitRoot, localRoot string) GitInterface {
	return &gitInterface{
		gitRoot:   gitRoot,
		localRoot: localRoot,
	}
}
func (g gitInterface) path(repo ...string) string {
	return path.Join(append([]string{g.gitRoot}, repo...)...)
}
func (g gitInterface) GetRepository(repo string) (PacakRepo, error) {
	r, err := git.OpenRepository(g.path(repo))
	if err != nil {
		return nil, fmt.Errorf("OpenRepository: %v", err)
	}
	return &pacakRepo{
		r:         r,
		localPath: path.Join(g.localRoot, repo),
	}, nil
}
func (g gitInterface) InitRepository(committer git.Signature, repo string, files []GitFile) error {
	repoPath := g.path(repo)
	if err := git.InitRepository(repoPath, true); err != nil {
		return fmt.Errorf("InitRepository: %v", err)
	}
	tmpDir := path.Join(os.TempDir(), "pacak-init-"+strings.Replace(repo, "/", "-", -1)+"-"+strconv.FormatInt(time.Now().UnixNano(), 16))
	err := os.MkdirAll(tmpDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("InitRepository: Failed create init directory - %v", err)
	}
	defer os.RemoveAll(tmpDir)
	_, stderr, err := process.Exec(
		fmt.Sprintf("initRepository(git clone): %s", repo), "git", "clone", repoPath, tmpDir)
	if err != nil {
		return fmt.Errorf("git clone: %v - %s", err, stderr)
	}
	for _, f := range files {
		dir := path.Dir(f.Path)
		if dir != "" {
			dir = path.Join(tmpDir, dir)
			os.MkdirAll(dir, os.ModePerm)
		}
		filePath := path.Join(tmpDir, f.Path)
		if err := ioutil.WriteFile(filePath, f.Data, 0666); err != nil {
			return fmt.Errorf("WriteFile: failed write file - %v", err)
		}
	}
	f, err := os.Create(path.Join(tmpDir, ".gitignore"))
	if err != nil {
		return fmt.Errorf("InitRepository: failed create init directory - %v", err)
	}
	defer f.Close()
	return initRepoCommit(tmpDir, &committer)
}

func initRepoCommit(tmpPath string, sig *git.Signature) (err error) {
	var stderr string
	if _, stderr, err = process.ExecDir(-1,
		tmpPath, fmt.Sprintf("initRepoCommit (git add): %s", tmpPath),
		"git", "add", "--all"); err != nil {
		return fmt.Errorf("git add: %s", stderr)
	}

	if _, stderr, err = process.ExecDir(-1,
		tmpPath, fmt.Sprintf("initRepoCommit (git commit): %s", tmpPath),
		"git", "commit", fmt.Sprintf("--author='%s <%s>'", sig.Name, sig.Email),
		"-m", "Initial commit"); err != nil {
		return fmt.Errorf("git commit: %s", stderr)
	}

	if _, stderr, err = process.ExecDir(-1,
		tmpPath, fmt.Sprintf("initRepoCommit (git push): %s", tmpPath),
		"git", "push", "origin", "master"); err != nil {
		return fmt.Errorf("git push: %s", stderr)
	}
	return nil
}

func (p pacakRepo) Save(committer git.Signature, message string, oldBrach, newBranch string, files []GitFile) (string, error) {
	repoWorkingPool.CheckIn(p.r.Path)
	defer repoWorkingPool.CheckOut(p.r.Path)

	if err := p.DiscardLocalRepoBranchChanges(oldBrach); err != nil {
		return "", fmt.Errorf("DiscardLocalRepoBranchChanges [branch: %s]: %v", oldBrach, err)
	} else if err = p.UpdateLocalCopyBranch(oldBrach); err != nil {
		return "", fmt.Errorf("UpdateLocalCopyBranch [branch: %s]: %v", oldBrach, err)
	}
	repoPath := p.r.Path
	localPath := p.localPath
	if oldBrach != newBranch {
		// Directly return error if new branch already exists in the server
		if git.IsBranchExist(repoPath, newBranch) {
			return "", errors.BranchAlreadyExists{newBranch}
		}

		// Otherwise, delete branch from local copy in case out of sync
		if git.IsBranchExist(localPath, newBranch) {
			if err := git.DeleteBranch(localPath, newBranch, git.DeleteBranchOptions{
				Force: true,
			}); err != nil {
				return "", fmt.Errorf("DeleteBranch [name: %s]: %v", newBranch, err)
			}
		}

		if err := p.CheckoutNewBranch(oldBrach, newBranch); err != nil {
			return "", fmt.Errorf("CheckoutNewBranch [old_branch: %s, new_branch: %s]: %v", oldBrach, newBranch, err)
		}
	}
	for _, f := range files {
		dir := path.Dir(f.Path)
		if dir != "" {
			dir = path.Join(localPath, dir)
			os.MkdirAll(dir, os.ModePerm)
		}
		filePath := path.Join(localPath, f.Path)
		if err := ioutil.WriteFile(filePath, f.Data, 0666); err != nil {
			return "", fmt.Errorf("WriteFile: failed write file - %v", err)
		}
	}

	if err := git.AddChanges(localPath, true); err != nil {
		return "", fmt.Errorf("git add --all: %v", err)
	} else if err = git.CommitChanges(localPath, git.CommitChangesOptions{
		Committer: &committer,
		Message:   message,
	}); err != nil {
		return "", fmt.Errorf("CommitChanges: %v", err)
	} else if err = git.Push(localPath, "origin", newBranch); err != nil {
		return "", fmt.Errorf("git push origin %s: %v", newBranch, err)
	}
	commit, err := p.r.GetBranchCommit(newBranch)
	if err != nil {
		return "", fmt.Errorf("Read last commit error %v", err)
	}
	return commit.ID.String(), nil
}

func (repo *pacakRepo) DiscardLocalRepoBranchChanges(branch string) error {
	if !util.IsExist(repo.localPath) {
		return nil
	}
	// No need to check if nothing in the repository.
	if !git.IsBranchExist(repo.localPath, branch) {
		return nil
	}

	refName := "origin/" + branch
	if err := git.ResetHEAD(repo.localPath, true, refName); err != nil {
		return fmt.Errorf("git reset --hard %s: %v", refName, err)
	}
	return nil
}

func (p *pacakRepo) CheckoutNewBranch(oldBranch, newBranch string) error {
	if err := git.Checkout(p.localPath, git.CheckoutOptions{
		Timeout:   pullTimeout,
		Branch:    newBranch,
		OldBranch: oldBranch,
	}); err != nil {
		return fmt.Errorf("git checkout -b %s %s: %v", newBranch, oldBranch, err)
	}
	return nil
}

// UpdateLocalCopyBranch makes sure local copy of repository in given branch is up-to-date.
func (p *pacakRepo) UpdateLocalCopyBranch(branch string) error {
	if !util.IsExist(p.localPath) {
		if err := git.Clone(p.r.Path, p.localPath, git.CloneRepoOptions{
			Timeout: cloneTimeout,
			Branch:  branch,
		}); err != nil {
			return fmt.Errorf("git clone %s: %v", branch, err)
		}
	} else {
		if err := git.Fetch(p.localPath, git.FetchRemoteOptions{
			Prune: true,
		}); err != nil {
			return fmt.Errorf("git fetch: %v", err)
		}
		if err := git.Checkout(p.localPath, git.CheckoutOptions{
			Branch: branch,
		}); err != nil {
			return fmt.Errorf("git checkout %s: %v", branch, err)
		}

		// Reset to align with remote in case of force push.
		if err := git.ResetHEAD(p.localPath, true, "origin/"+branch); err != nil {
			return fmt.Errorf("git reset --hard origin/%s: %v", branch, err)
		}
	}
	return nil
}
