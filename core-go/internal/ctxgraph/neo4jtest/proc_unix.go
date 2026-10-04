//go:build !windows

package neo4jtest

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// prepare puts the launcher in its own process group so the whole JVM tree can be killed.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// afterStart has nothing to do on Unix: orphans are reaped by reapStale on the next start.
func afterStart(_ *exec.Cmd) error { return nil }

// processAlive reports whether pid is a running process.
func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// processStartToken identifies one incarnation of pid by its start time (field 22 of /proc/<pid>/stat), so a
// recycled PID never matches a recorded token. Empty where /proc is unavailable: then nothing is ever killed.
func processStartToken(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	s := string(b)
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return ""
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) < 20 || fields[0] == "Z" {
		return ""
	}
	return fields[19] // starttime: field 22 overall, the 20th after "pid (comm)"
}

// killPidTree kills the process group led by pid.
func killPidTree(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		return syscall.Kill(pid, syscall.SIGKILL)
	}
	return nil
}

// killTree kills the launcher and every child JVM it started.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
