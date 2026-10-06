package demorun

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// Process is one running process: what the orphan sweep needs.
type Process struct {
	PID         int
	Name        string
	CommandLine string
}

func runtimeFoldsCase() bool { return runtime.GOOS == "windows" }

// normalizePath makes a path comparable inside a command line: forward slashes and, where the file system folds case, lower case.
func normalizePath(p string) string {
	p = strings.ReplaceAll(filepath.Clean(p), `\`, "/")
	if runtimeFoldsCase() {
		p = strings.ToLower(p)
	}
	return p
}

// OrphanPIDs are the java processes whose command line points under dir (the demo's own Neo4j directory). A JVM that outlived
// its recorded PID (the PID file names the launcher, not the child) still holds the database and its port; a Neo4j of another
// directory, or a sibling directory that merely shares the prefix, is never matched.
func OrphanPIDs(procs []Process, dir string) []int {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	want := normalizePath(dir)
	var out []int
	for _, p := range procs {
		if p.PID <= 0 || !strings.HasPrefix(strings.ToLower(strings.TrimSuffix(p.Name, ".exe")), "java") {
			continue
		}
		if commandPointsUnder(normalizePath2(p.CommandLine), want) {
			out = append(out, p.PID)
		}
	}
	return out
}

// normalizePath2 is normalizePath for a whole command line (which is not a clean path).
func normalizePath2(s string) string {
	s = strings.ReplaceAll(s, `\`, "/")
	if runtimeFoldsCase() {
		s = strings.ToLower(s)
	}
	return s
}

// commandPointsUnder reports whether the command line names dir or a path below it.
func commandPointsUnder(cmd, dir string) bool {
	for from := 0; ; {
		i := strings.Index(cmd[from:], dir)
		if i < 0 {
			return false
		}
		end := from + i + len(dir)
		if end == len(cmd) || strings.ContainsRune(`/ "'`, rune(cmd[end])) {
			return true
		}
		from = end
	}
}

// SweepOrphans kills every orphaned Neo4j JVM under dir and returns how many it killed. list and kill are injectable; the
// production pair is ListProcesses and KillTree. A failed kill does not stop the others, but is reported.
func SweepOrphans(ctx context.Context, list func(context.Context) ([]Process, error), kill func(pid int) error, dir string) (int, error) {
	procs, err := list(ctx)
	if err != nil {
		return 0, fmt.Errorf("demorun: list processes to find orphaned Neo4j JVMs: %w", err)
	}
	killed := 0
	var failures []error
	for _, pid := range OrphanPIDs(procs, dir) {
		if err := kill(pid); err != nil {
			failures = append(failures, fmt.Errorf("demorun: stop orphaned Neo4j JVM %d: %w", pid, err))
			continue
		}
		killed++
	}
	return killed, errors.Join(failures...)
}
