package main

import (
	"flag"
	"github.com/kuberlab/pacak/pkg/pacakimpl"
	"os"
	"path"
	"github.com/kuberlab/pacak/pkg/api"
)

func main() {
	gitPath := flag.String("git-data-path", "/pacak-data", "Path to store bare git repos")
	localPath := flag.String("local-data-path", path.Join(os.TempDir(), "pacak-work-data"), "Path for local copy git directory. Used for commits")
	git := pacakimpl.NewGitInterface(*gitPath, *localPath)
	api.StartAPI(git)
}
