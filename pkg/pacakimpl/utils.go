package pacakimpl

import (
	"fmt"
	git "github.com/gogits/git-module"
	"github.com/kuberlab/pacak/pkg/process"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"github.com/kuberlab/pacak/pkg/util"
	"log"
	"path"
	"io/ioutil"
	"github.com/kuberlab/pacak/pkg/errors"
)

var pullTimeout time.Duration

func init() {
	pullTimeout = time.Minute
}
type GitInterface interface {
	InitRepository(repo string) error
	GetRepository(repo string) (PacakRepo,error)
}

type PacakRepo interface {
	InitRepository(repo string) error
}

type pacakRepo struct {
	r *git.Repository
	localPath string
}

type gitInterface struct {
	root string
}

func (g gitInterface) path(repo ...string) string {
	return filepath.Join(g.root, repo...)
}
func (g gitInterface) GetRepository(repo string) (PacakRepo,error) {
	r,err := git.OpenRepository(g.path(repo))
	if err!=nil{
		return nil,fmt.Errorf("OpenRepository: %v", err)
	}
	return &pacakRepo{
		r: r,
	}
}
func (g gitInterface) InitRepository(repo string) error {
	if err := git.InitRepository(g.path(repo), true); err != nil {
		return fmt.Errorf("InitRepository: %v", err)
	}
	tmpDir := filepath.Join(os.TempDir(), "pacak-init-"+strings.Replace(repo, "/", "-", -1)+"-"-strconv.FormatInt(time.Now().Nanosecond(), 16))
	err := os.MkdirAll(tmpDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("InitRepository: Failed create init directory - %v", err)
	}
	defer os.RemoveAll(tmpDir)
	_, stderr, err := process.Exec(
		fmt.Sprintf("initRepository(git clone): %s", repo), "git", "clone", repo, tmpDir)
	if err != nil {
		return fmt.Errorf("git clone: %v - %s", err, stderr)
	}
	f, err := os.Create(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		return fmt.Errorf("InitRepository: failed create init directory - %v", err)
	}
	defer f.Close()
	return initRepoCommit(tmpDir, &git.Signature{
		Name:  "pacack",
		Email: "qu@paluk.space",
		When:  time.Now(),
	})
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

func (p pacakRepo) Save(oldBrach,newBranch string,files []GitFile) (string,error){
	repoPath := p.r.Path
	localPath := p.localPath

	if oldBrach!=newBranch {
		// Directly return error if new branch already exists in the server
		if git.IsBranchExist(repoPath, newBranch) {
			return errors.BranchAlreadyExists{newBranch}
		}

		// Otherwise, delete branch from local copy in case out of sync
		if git.IsBranchExist(localPath, newBranch) {
			if err = git.DeleteBranch(localPath, newBranch, git.DeleteBranchOptions{
				Force: true,
			}); err != nil {
				return fmt.Errorf("DeleteBranch [name: %s]: %v", newBranch, err)
			}
		}

		if err := p.CheckoutNewBranch(oldBrach,newBranch); err != nil {
			return fmt.Errorf("CheckoutNewBranch [old_branch: %s, new_branch: %s]: %v", opts.OldBranch, opts.NewBranch, err)
		}
	}

	oldFilePath := path.Join(localPath, opts.OldTreeName)
	filePath := path.Join(localPath, opts.NewTreeName)
	os.MkdirAll(path.Dir(filePath), os.ModePerm)

	// If it's meant to be a new file, make sure it doesn't exist.
	if opts.IsNewFile {
		if com.IsExist(filePath) {
			return ErrRepoFileAlreadyExist{filePath}
		}
	}

	// Ignore move step if it's a new file under a directory.
	// Otherwise, move the file when name changed.
	if com.IsFile(oldFilePath) && opts.OldTreeName != opts.NewTreeName {
		if err = git.MoveFile(localPath, opts.OldTreeName, opts.NewTreeName); err != nil {
			return fmt.Errorf("git mv %s %s: %v", opts.OldTreeName, opts.NewTreeName, err)
		}
	}

	if err = ioutil.WriteFile(filePath, []byte(opts.Content), 0666); err != nil {
		return fmt.Errorf("WriteFile: %v", err)
	}

	if err = git.AddChanges(localPath, true); err != nil {
		return fmt.Errorf("git add --all: %v", err)
	} else if err = git.CommitChanges(localPath, git.CommitChangesOptions{
		Committer: doer.NewGitSig(),
		Message:   opts.Message,
	}); err != nil {
		return fmt.Errorf("CommitChanges: %v", err)
	} else if err = git.Push(localPath, "origin", opts.NewBranch); err != nil {
		return fmt.Errorf("git push origin %s: %v", opts.NewBranch, err)
	}

	gitRepo, err := git.OpenRepository(repo.RepoPath())
	if err != nil {
		log.Error(2, "OpenRepository: %v", err)
		return nil
	}
	commit, err := gitRepo.GetBranchCommit(opts.NewBranch)
	if err != nil {
		log.Error(2, "GetBranchCommit [branch: %s]: %v", opts.NewBranch, err)
		return nil
	}

	// Simulate push event.
	pushCommits := &PushCommits{
		Len:     1,
		Commits: []*PushCommit{CommitToPushCommit(commit)},
	}
	oldCommitID := opts.LastCommitID
	if opts.NewBranch != opts.OldBranch {
		oldCommitID = git.EMPTY_SHA
	}
	if err := CommitRepoAction(CommitRepoActionOptions{
		PusherName:  doer.Name,
		RepoOwnerID: repo.MustOwner().ID,
		RepoName:    repo.Name,
		RefFullName: git.BRANCH_PREFIX + opts.NewBranch,
		OldCommitID: oldCommitID,
		NewCommitID: commit.ID.String(),
		Commits:     pushCommits,
	}); err != nil {
		log.Error(2, "CommitRepoAction: %v", err)
		return nil
	}

	go AddTestPullRequestTask(doer, repo.ID, opts.NewBranch, true)
	return nil
}

func (repo *pacakRepo) localpath() error {
	return repo.r.Path
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