package errors

import "fmt"

type BranchAlreadyExists struct {
	Name string
}

func IsBranchAlreadyExists(err error) bool {
	_, ok := err.(BranchAlreadyExists)
	return ok
}

func (err BranchAlreadyExists) Error() string {
	return fmt.Sprintf("branch already exists [name: %s]", err.Name)
}
