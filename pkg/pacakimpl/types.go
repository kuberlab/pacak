package pacakimpl

import "time"

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
