package neo4jtest

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

const helperEnv = "GHOST_NEO4JTEST_HELPER"

// TestHelperProcess is not a real test: the tests below re-run the test binary in helper modes.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv(helperEnv) {
	case "sleep":
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	case "owner":
		// Start a long-lived child, put it in the kill-on-close job, report its pid, then wait to be killed.
		child := helperCmd("sleep")
		if err := child.Start(); err != nil {
			fmt.Println("ERR", err)
			os.Exit(1)
		}
		if err := afterStart(child); err != nil {
			fmt.Println("ERR", err)
			os.Exit(1)
		}
		fmt.Println(child.Process.Pid)
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
}

func helperCmd(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	return cmd
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still alive", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("short process: %v", err)
	}
	return cmd.ProcessState.Pid()
}

func TestReapStaleKillsAnOrphanedServerAndRemovesItsDirectory(t *testing.T) {
	orphan := helperCmd("sleep")
	prepare(orphan)
	if err := orphan.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = killTree(orphan); _ = orphan.Wait() })
	dir, err := os.MkdirTemp("", runDirPrefix+"reaptest-")
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("%d %d %s\n", deadPid(t), orphan.Process.Pid, processStartToken(orphan.Process.Pid))
	if err := os.WriteFile(filepath.Join(dir, ownerFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := reapStale(); n < 1 {
		t.Fatalf("reapStale reaped %d, want at least 1", n)
	}
	_ = orphan.Wait() // reap the killed child so it is not a zombie on Unix
	waitDead(t, orphan.Process.Pid)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("run directory still exists: %v", err)
	}
}

func TestReapStaleLeavesALiveOwnersServerAlone(t *testing.T) {
	server := helperCmd("sleep")
	prepare(server)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = killTree(server); _ = server.Wait() })
	dir, err := os.MkdirTemp("", runDirPrefix+"livetest-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	body := fmt.Sprintf("%d %d %s\n", os.Getpid(), server.Process.Pid, processStartToken(server.Process.Pid))
	if err := os.WriteFile(filepath.Join(dir, ownerFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	reapStale()
	if !processAlive(server.Process.Pid) {
		t.Fatal("a server whose owner is alive was killed")
	}
}

func TestReapStaleNeverKillsARecycledPid(t *testing.T) {
	bystander := helperCmd("sleep")
	prepare(bystander)
	if err := bystander.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = killTree(bystander); _ = bystander.Wait() })
	dir, err := os.MkdirTemp("", runDirPrefix+"recycledtest-")
	if err != nil {
		t.Fatal(err)
	}
	// The recorded launcher PID now belongs to an unrelated process: its start token differs.
	body := fmt.Sprintf("%d %d %s\n", deadPid(t), bystander.Process.Pid, "123")
	if err := os.WriteFile(filepath.Join(dir, ownerFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	reapStale()
	if !processAlive(bystander.Process.Pid) {
		t.Fatal("reapStale killed a process whose PID was recycled")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stale run directory was not removed: %v", err)
	}
}

func TestKillOnCloseJobKillsTheServerWhenItsOwnerDies(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the kill-on-close job is Windows-only; Unix relies on reapStale")
	}
	owner := helperCmd("owner")
	out, err := owner.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		_ = owner.Process.Kill()
		t.Fatalf("read child pid: %v", err)
	}
	child, err := strconv.Atoi(line[:len(line)-1])
	if err != nil {
		_ = owner.Process.Kill()
		t.Fatalf("helper said %q", line)
	}
	if !processAlive(child) {
		t.Fatal("child is not running")
	}
	// Kill the owner the hard way (no cleanup runs): the job handle closes and Windows kills the child.
	if err := owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = owner.Wait()
	waitDead(t, child)
}
