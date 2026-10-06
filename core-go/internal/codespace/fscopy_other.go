//go:build !windows

package codespace

// nativeCopyFile has no operating-system fast path here: the caller copies with a read and write loop.
func nativeCopyFile(string, string) (handled bool, err error) { return false, nil }
