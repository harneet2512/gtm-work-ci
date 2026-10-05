//go:build windows

package neo4jtest

import (
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"
)

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW        = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob      = kernel32.NewProc("AssignProcessToJobObject")
	procGetExitCodeProcess      = kernel32.NewProc("GetExitCodeProcess")
	procGetProcessTimes         = kernel32.NewProc("GetProcessTimes")

	jobOnce   sync.Once
	jobHandle syscall.Handle
	jobErr    error
)

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x2000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
	processQueryLimitedInformation    = 0x1000
	stillActive                       = 259
)

type ioCounters struct{ a, b, c, d, e, f uint64 }

type basicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type extendedLimitInformation struct {
	Basic                 basicLimitInformation
	IO                    ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// killOnCloseJob is one Job Object per test process with KILL_ON_JOB_CLOSE. Its handle is never closed
// explicitly: when the test process exits for any reason (timeout, crash, killed by the OS), Windows closes
// the handle and terminates every process in the job, so no Neo4j JVM outlives the test that started it.
func killOnCloseJob() (syscall.Handle, error) {
	jobOnce.Do(func() {
		h, _, err := procCreateJobObjectW.Call(0, 0)
		if h == 0 {
			jobErr = fmt.Errorf("neo4jtest: CreateJobObject: %w", err)
			return
		}
		info := extendedLimitInformation{Basic: basicLimitInformation{LimitFlags: jobObjectLimitKillOnJobClose}}
		ok, _, err := procSetInformationJobObject.Call(h, jobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
		if ok == 0 {
			_ = syscall.CloseHandle(syscall.Handle(h))
			jobErr = fmt.Errorf("neo4jtest: SetInformationJobObject: %w", err)
			return
		}
		jobHandle = syscall.Handle(h)
	})
	return jobHandle, jobErr
}

func prepare(_ *exec.Cmd) {}

// afterStart puts the launcher into the kill-on-close job right after it starts. Child JVMs the launcher
// spawns later inherit the job. The launcher is a JVM that takes far longer to boot than this call, so its
// server child cannot be spawned before the assignment.
func afterStart(cmd *exec.Cmd) error {
	job, err := killOnCloseJob()
	if err != nil {
		return err
	}
	ph, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("neo4jtest: open launcher process: %w", err)
	}
	defer syscall.CloseHandle(ph)
	if ok, _, err := procAssignProcessToJob.Call(uintptr(job), uintptr(ph)); ok == 0 {
		return fmt.Errorf("neo4jtest: AssignProcessToJobObject: %w", err)
	}
	return nil
}

// processAlive reports whether pid is a running process.
func processAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if ok, _, _ := procGetExitCodeProcess.Call(uintptr(h), uintptr(unsafe.Pointer(&code))); ok == 0 {
		return false
	}
	return code == stillActive
}

// processStartToken identifies one incarnation of pid by its creation time, so a recycled PID never matches
// a recorded token. Empty when the process is gone or unreadable.
func processStartToken(pid int) string {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)
	var creation, exit, kernel, user syscall.Filetime
	ok, _, _ := procGetProcessTimes.Call(uintptr(h), uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if ok == 0 {
		return ""
	}
	return strconv.FormatInt(creation.Nanoseconds(), 10)
}

// killPidTree kills pid and its descendants.
func killPidTree(pid int) error {
	out, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		return &killError{out: string(out), err: err}
	}
	return nil
}

// killTree kills the launcher and every child JVM it started (taskkill /T walks the process tree).
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := killPidTree(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	return nil
}

type killError struct {
	out string
	err error
}

func (e *killError) Error() string { return "neo4jtest: taskkill: " + e.err.Error() + ": " + e.out }
func (e *killError) Unwrap() error { return e.err }
