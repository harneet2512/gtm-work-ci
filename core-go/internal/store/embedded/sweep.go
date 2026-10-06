package embedded

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Why clusters leaked: a test binary that is killed (go test -timeout, a cancelled run, a crashed agent) never runs its
// deferred stop, and the postmaster it started keeps running with its data under <tmp>/ghost-embedded-pg-<port> for
// days. Launch therefore records its owner process and sweeps clusters whose owner is gone.

const (
	clusterPrefix = "ghost-embedded-pg-"
	ownerFile     = "owner.pid"
	// ownerlessAge is how old a cluster without an owner file (made before this file existed) must be before it is
	// treated as stale; a test run is far shorter, so a younger one may still be starting.
	ownerlessAge = 2 * time.Hour
)

// SweepStale stops and removes every cluster under the OS temp dir whose owning process is gone. It never touches a
// cluster of a live process (parallel packages and parallel agents share the temp dir). It returns how many it removed.
func SweepStale() int {
	return sweepStale(os.TempDir(), time.Now(), processAlive, killProcess)
}

func sweepStale(tmp string, now time.Time, alive func(pid int) bool, kill func(pid int, dataDir string)) int {
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return 0
	}
	swept := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), clusterPrefix) {
			continue
		}
		root := filepath.Join(tmp, e.Name())
		if !isStale(root, now, alive) {
			continue
		}
		if pm := readPID(filepath.Join(root, "data", "postmaster.pid")); pm > 0 {
			kill(pm, filepath.Join(root, "data"))
		}
		if os.RemoveAll(root) == nil {
			swept++
		}
	}
	return swept
}

func isStale(root string, now time.Time, alive func(int) bool) bool {
	if owner := readPID(filepath.Join(root, ownerFile)); owner > 0 {
		return !alive(owner)
	}
	info, err := os.Stat(root)
	return err == nil && now.Sub(info.ModTime()) > ownerlessAge
}

// readPID reads the first line of a pid file; 0 when it is missing or malformed.
func readPID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	first, _, _ := strings.Cut(string(b), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// writeOwner records this process as the cluster's owner.
func writeOwner(root string) error {
	return os.WriteFile(filepath.Join(root, ownerFile), []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" { // FindProcess opens the process there, so success means it exists
		_ = p.Release()
		return true
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// killProcess stops pid only when it is the postgres postmaster of dataDir: a dead postmaster's pid may since have been
// given to an unrelated process, which must never be killed.
func killProcess(pid int, dataDir string) {
	name, cmd, err := processInfo(pid)
	if err != nil || !isClusterPostmaster(name, cmd, dataDir) {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
		_ = p.Release()
	}
}

// isClusterPostmaster says whether a process (its image name and command line) is a postgres running dataDir.
func isClusterPostmaster(name, cmdline, dataDir string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "\\", "/")) }
	if !strings.Contains(norm(name), "postgres") || dataDir == "" {
		return false
	}
	return strings.Contains(norm(cmdline), norm(dataDir))
}

// processInfo reads a process's image name and command line.
func processInfo(pid int) (name, cmdline string, err error) {
	if runtime.GOOS == "windows" {
		script := fmt.Sprintf("$p = Get-CimInstance Win32_Process -Filter 'ProcessId=%d'; if ($p) { $p.Name; $p.CommandLine }", pid)
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
		if err != nil {
			return "", "", err
		}
		lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
		if len(lines) < 2 {
			return "", "", errors.New("embedded: process not found")
		}
		return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), nil
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		out, perr := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
		if perr != nil {
			return "", "", perr
		}
		raw = out
	}
	cmdline = strings.ReplaceAll(string(raw), "\x00", " ")
	return filepath.Base(strings.Fields(cmdline + " ")[0]), cmdline, nil
}
