package pacakimpl

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	git "github.com/gogits/git-module"
	"github.com/kuberlab/pacak/pkg/errors"
	"github.com/kuberlab/pacak/pkg/process"
	"github.com/kuberlab/pacak/pkg/sync"
	"github.com/kuberlab/pacak/pkg/util"
	"io"
	//"path/filepath"
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
	ExistsRepository(repo string) bool
	DeleteRepository(repo string) error
}

type PacakRepo interface {
	Save(committer git.Signature, message string, oldBrach, newBranch string, files []GitFile) (string, error)
	CheckoutAndSave(committer git.Signature, message string, revision, newBranch string, files []GitFile) (string, error)
	Commits(branch string, filter func(string) bool) ([]Commit, error)
	Checkout(ref string) error
	PushTag(tag string, fromRef string, override bool) error
	IsTagExists(tag string) bool
	TagList() ([]string, error)
	DeleteTag(tag string) error
	GetFileAtRev(rev, path string) (io.Reader, error)
	GetRev(rev string) (*git.Commit, error)
	//GetTreeAtRev(rev string) ([]GitFile, error)
}

type pacakRepo struct {
	R         *git.Repository
	LocalPath string
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
func (g gitInterface) ExistsRepository(repo string) bool {
	return util.IsExist(g.path(repo))
}
func (g gitInterface) DeleteRepository(repo string) error {
	err := os.RemoveAll(path.Join(g.localRoot, repo))
	if err != nil {
		return nil
	}
	err = os.RemoveAll(g.path(repo))
	if err != nil {
		return nil
	}
	return nil
}
func (g gitInterface) GetRepository(repo string) (PacakRepo, error) {
	r, err := git.OpenRepository(g.path(repo))
	if err != nil {
		return nil, fmt.Errorf("OpenRepository: %v", err)
	}
	return &pacakRepo{
		R:         r,
		LocalPath: path.Join(g.localRoot, repo),
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

func (p *pacakRepo) GetRev(rev string) (*git.Commit, error) {
	c, err := p.R.GetCommit(rev)
	if err != nil {
		return nil, fmt.Errorf("Failed read commit '%s' - %v", rev, err)
	}
	return c, err
}
func (p *pacakRepo) GetFileAtRev(rev, path string) (io.Reader, error) {
	c, err := p.R.GetCommit(rev)
	if err != nil {
		return nil, fmt.Errorf("Failed read commit '%s' - %v", rev, err)
	}
	b, err := c.GetBlobByPath(path)
	if err != nil {
		return nil, fmt.Errorf("Failed read file '%s' - %v", rev, err)
	}
	return b.Data()
}

/*func (p *pacakRepo) GetTreeAtRev(rev string) ([]GitFile, error) {
	c, err := p.R.GetCommit(rev)
	if err != nil {
		return nil, fmt.Errorf("Failed read commit '%s' - %v", rev, err)
	}
	files := []GitFile{}
	files, err = readFullTree(c, "", files)
	if err != nil {
		return nil, fmt.Errorf("Failed read tree '%s' - %v", rev, err)
	}
	return files, nil
}

func readFullTree(c *git.Commit, path string, files []GitFile) ([]GitFile, error) {
	var entries git.Entries
	var err error
	if path == "" {
		entries, err = c.Tree.ListEntries()
	} else {
		if !strings.HasSuffix(path, "/") {
			path = path + "/"
		}
		tree, err := c.Tree.GetTreeEntryByPath(path)

	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		fp := filepath.Join(path, e.Name())
		if e.IsDir() {
			files, err = readFullTree(c, fp, files)
			if err != nil {
				return nil
			}
		} else if e.Type == git.OBJECT_BLOB && !e.IsLink() && !e.IsSubModule() {
			files = append(files, GitFile{
				Path: fp,
			})
		}
	}
	return files, nil
}*/

func (p *pacakRepo) Checkout(ref string) error {
	return git.Checkout(p.LocalPath, git.CheckoutOptions{Branch: ref})
}

func (p *pacakRepo) IsTagExists(tag string) bool {
	return p.R.IsTagExist(tag)
}

func (p *pacakRepo) TagList() ([]string, error) {
	return p.R.GetTags()
}

func (p *pacakRepo) DeleteTag(tag string) error {
	// delete tag locally
	cmd := git.NewCommand("tag", "-d", tag)
	_, err := cmd.RunInDir(p.LocalPath)
	if err != nil {
		return err
	}

	// delete tag on the remote side
	return git.Push(p.LocalPath, "origin", fmt.Sprintf(":refs/tags/%v", tag))
}

func (p *pacakRepo) PushTag(tag string, fromRef string, override bool) error {
	if override && p.R.IsTagExist(tag) {
		if err := p.DeleteTag(tag); err != nil {
			return err
		}
	}

	// Create a new tag locally (not via *git.Repository! since it uses bare git path).
	cmd := git.NewCommand("tag", tag, fromRef)
	_, err := cmd.RunInDir(p.LocalPath)
	if err != nil {
		return err
	}

	// Push a new tag.
	return git.Push(p.LocalPath, "origin", tag)
}

func (p *pacakRepo) CheckoutAndSave(committer git.Signature, message string, revision, newBranch string, files []GitFile) (string, error) {
	repoWorkingPool.CheckIn(p.R.Path)
	defer repoWorkingPool.CheckOut(p.R.Path)
	repoPath := p.R.Path
	localPath := p.LocalPath
	if err := git.ResetHEAD(p.LocalPath, true, revision); err != nil {
		return fmt.Errorf("git reset --hard %s: %v", revision, err)
	}
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

	if err := p.CheckoutNewBranch("", newBranch); err != nil {
		return "", fmt.Errorf("CheckoutNewBranch [new_branch: %s]: %v", newBranch, err)
	}

	return save(p.R, localPath, committer, message, newBranch, files)
}
func save(repo *git.Repository, localPath string, committer git.Signature, message string, newBranch string, files []GitFile) (string, error) {
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
	commit, err := repo.GetBranchCommit(newBranch)
	if err != nil {
		return "", fmt.Errorf("Read last commit error %v", err)
	}
	return commit.ID.String(), nil
}
func (p *pacakRepo) Save(committer git.Signature, message string, oldBrach, newBranch string, files []GitFile) (string, error) {
	repoWorkingPool.CheckIn(p.R.Path)
	defer repoWorkingPool.CheckOut(p.R.Path)

	if err := p.DiscardLocalRepoBranchChanges(oldBrach); err != nil {
		return "", fmt.Errorf("DiscardLocalRepoBranchChanges [branch: %s]: %v", oldBrach, err)
	} else if err = p.UpdateLocalCopyBranch(oldBrach); err != nil {
		return "", fmt.Errorf("UpdateLocalCopyBranch [branch: %s]: %v", oldBrach, err)
	}
	repoPath := p.R.Path
	localPath := p.LocalPath
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
	return save(p.R, localPath, committer, message, newBranch, files)
}

func (p *pacakRepo) DiscardLocalRepoBranchChanges(branch string) error {
	if !util.IsExist(p.LocalPath) {
		return nil
	}
	// No need to check if nothing in the repository.
	if !git.IsBranchExist(p.LocalPath, branch) {
		return nil
	}

	refName := "origin/" + branch
	if err := git.ResetHEAD(p.LocalPath, true, refName); err != nil {
		return fmt.Errorf("git reset --hard %s: %v", refName, err)
	}
	return nil
}

func (p *pacakRepo) CheckoutNewBranch(oldBranch, newBranch string) error {
	if err := git.Checkout(p.LocalPath, git.CheckoutOptions{
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
	if !util.IsExist(p.LocalPath) {
		if err := git.Clone(p.R.Path, p.LocalPath, git.CloneRepoOptions{
			Timeout: cloneTimeout,
			Branch:  branch,
		}); err != nil {
			return fmt.Errorf("git clone %s: %v", branch, err)
		}
	} else {
		if err := git.Fetch(p.LocalPath, git.FetchRemoteOptions{
			Prune: true,
		}); err != nil {
			return fmt.Errorf("git fetch: %v", err)
		}
		if err := git.Checkout(p.LocalPath, git.CheckoutOptions{
			Branch: branch,
		}); err != nil {
			return fmt.Errorf("git checkout %s: %v", branch, err)
		}

		// Reset to align with remote in case of force push.
		if err := git.ResetHEAD(p.LocalPath, true, "origin/"+branch); err != nil {
			return fmt.Errorf("git reset --hard origin/%s: %v", branch, err)
		}
	}
	return nil
}

type commitElement struct {
	v    *git.Commit
	next *commitElement
}
type commitList struct {
	e *commitElement
}

func (l *commitList) Poll() *git.Commit {
	if l.e == nil {
		return nil
	}
	v := l.e.v
	l.e = l.e.next
	return v
}
func (l *commitList) Add(v *git.Commit) {
	top := &commitElement{
		v: v,
	}
	if l.e != nil {
		top.next = l.e
	}
	l.e = top
}

func (p *pacakRepo) Commits(branch string, filter func(string) bool) ([]Commit, error) {
	var branches []string
	var err error
	if branch != "" {
		branches = []string{branch}
	} else {
		branches, err = p.R.GetBranches()
		if err != nil {
			return nil, fmt.Errorf("git list branch failed - %v", err)
		}
	}
	added := map[string]bool{}
	commits := []Commit{}
	maybeAdd := func(c *git.Commit) bool {
		if !filter(c.CommitMessage) {
			return true
		}
		if _, ok := added[c.ID.String()]; ok {
			return false
		}
		added[c.ID.String()] = true
		parents := []string{}
		for i := 0; i < c.ParentCount(); i++ {
			p, _ := c.ParentID(i)
			parents = append(parents, p.String())
		}
		commits = append(commits, Commit{
			ID:          c.ID.String(),
			AuthorName:  c.Committer.Name,
			AuthorEmail: c.Committer.Email,
			Message:     c.CommitMessage,
			When:        c.Committer.When,
			Parents:     parents,
		})
		return true
	}
	for _, branch := range branches {
		c, err := p.R.GetBranchCommit(branch)
		if err != nil {
			return nil, fmt.Errorf("git get branch commit failed - %v", err)
		}
		list := &commitList{}
		list.Add(c)
		for c = list.Poll(); c != nil; c = list.Poll() {
			if !maybeAdd(c) {
				continue
			}
			for i := 0; i < c.ParentCount(); i++ {
				p, err := c.Parent(i)
				if err != nil {
					return nil, fmt.Errorf("git get parent commit failed - %v", err)
				}
				list.Add(p)
			}
		}

	}
	sort.Sort(CommitSorter(commits))
	return commits, nil
}
