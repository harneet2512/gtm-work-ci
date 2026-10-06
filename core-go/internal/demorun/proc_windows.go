//go:build windows

package demorun

import (
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

const (
	createNewProcessGroup   = 0x00000200
	createNoWindow          = 0x08000000
	processQueryLimitedInfo = 0x1000
	stillActive             = 259
	errorAccessDenied       = syscall.Errno(5)
)

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procGetExitCode = kernel32.NewProc("GetExitCodeProcess")
)

// detach makes the child outlive this command and the terminal that started it. It gets its own hidden console
// (CREATE_NO_WINDOW) that its console-subsystem children (java, pg_ctl, node) inherit, so none of them opens a
// window, and a process group of its own, so a Ctrl+C in the user's terminal is not delivered to it. It is
// deliberately not DETACHED_PROCESS: a console-less parent makes every console child allocate a new console, and
// that made restarted Neo4j and Postgres children die at startup with STATUS_CONTROL_C_EXIT (0xC000013A).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | createNoWindow}
}

// ProcessAlive reports whether pid is a running process.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInfo, false, uint32(pid))
	if err != nil {
		return err == errorAccessDenied // exists, but not ours to query
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if ok, _, _ := procGetExitCode.Call(uintptr(h), uintptr(unsafe.Pointer(&code))); ok == 0 {
		return false
	}
	return code == stillActive
}

// KillTree terminates pid and every descendant (taskkill /T walks the process tree: npm -> node -> next).
func KillTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	out, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if err != nil && ProcessAlive(pid) {
		return &killError{pid: pid, out: string(out), err: err}
	}
	return nil
}
