package pacakimpl

import (
	"os"
	"time"
)

type GitFile struct {
	Path string
	Data []byte
}

type Commit struct {
	AuthorName  string
	AuthorEmail string
	Message     string
	ID          string
	Parents     []string
	When        time.Time
}

type CommitSorter []Commit

func (s CommitSorter) Len() int {
	return len(s)
}
func (s CommitSorter) Less(i, j int) bool {
	return s[i].When.After(s[j].When)
}
func (s CommitSorter) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

type GitFileInfo struct {
	dir     bool
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func (fs *GitFileInfo) Name() string       { return fs.name }
func (fs *GitFileInfo) IsDir() bool        { return fs.dir }
func (fs *GitFileInfo) Size() int64        { return fs.size }
func (fs *GitFileInfo) Mode() os.FileMode  { return fs.mode }
func (fs *GitFileInfo) ModTime() time.Time { return fs.modTime }
func (fs *GitFileInfo) Sys() interface{}   { return nil }
