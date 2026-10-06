//go:build !windows

package demorun

import (
	"os/exec"
	"syscall"
	"time"
)

// detach starts the child in its own session so it outlives this command and its terminal.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// ProcessAlive reports whether pid is a running process.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// KillTree terminates the whole process group of pid (the child is a session leader, so its group id is its
// pid), politely first and then for good.
func KillTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	for i := 0; i < 20 && ProcessAlive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if ProcessAlive(pid) {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	if ProcessAlive(pid) {
		time.Sleep(200 * time.Millisecond)
	}
	if ProcessAlive(pid) {
		return &killError{pid: pid, out: "still alive after SIGKILL", err: syscall.ESRCH}
	}
	return nil
}
