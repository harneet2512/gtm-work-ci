package neo4jtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ownerFile records "<test pid> <launcher pid> <launcher start token>" in a run directory, so a later start
// can tell whether the test that started this server is gone, and kill the launcher only if that exact process
// (not a recycled PID) is still running.
const ownerFile = "owner.pid"

const runDirPrefix = "ghost-neo4j-"

func writeOwner(run string, launcherPid int) error {
	token := processStartToken(launcherPid)
	if token == "" {
		token = "-"
	}
	body := fmt.Sprintf("%d %d %s\n", os.Getpid(), launcherPid, token)
	if err := os.WriteFile(filepath.Join(run, ownerFile), []byte(body), 0o600); err != nil {
		return fmt.Errorf("neo4jtest: write owner: %w", err)
	}
	return nil
}

// reapStale kills servers whose owning test process is gone and removes their run directories. It is the
// backstop for platforms or races the kill-on-close job does not cover. It returns how many it reaped.
func reapStale() int {
	dirs, err := filepath.Glob(filepath.Join(os.TempDir(), runDirPrefix+"*"))
	if err != nil {
		return 0
	}
	reaped := 0
	for _, dir := range dirs {
		owner, launcher, token, ok := readOwner(dir)
		if !ok || processAlive(owner) {
			continue
		}
		// Kill only the exact launcher this test started: a recycled PID has a different start token.
		if launcher > 0 && token != "-" && processStartToken(launcher) == token {
			_ = killPidTree(launcher)
		}
		_ = removeAllRetry(dir)
		reaped++
	}
	return reaped
}

func readOwner(dir string) (owner, launcher int, token string, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, ownerFile))
	if err != nil {
		return 0, 0, "", false
	}
	fields := strings.Fields(string(b))
	if len(fields) != 3 {
		return 0, 0, "", false
	}
	o, err1 := strconv.Atoi(fields[0])
	l, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		return 0, 0, "", false
	}
	return o, l, fields[2], true
}
