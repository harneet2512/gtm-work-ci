package demorun

import "fmt"

type killError struct {
	pid int
	out string
	err error
}

func (e *killError) Error() string {
	return fmt.Sprintf("demorun: kill process tree of pid %d: %v: %s", e.pid, e.err, e.out)
}

func (e *killError) Unwrap() error { return e.err }
