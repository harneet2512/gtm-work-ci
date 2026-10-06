package embedded

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type sweepRig struct {
	tmp    string
	alive  map[int]bool
	killed []int
	dirs   []string
	now    time.Time
}

func newSweepRig(t *testing.T) *sweepRig {
	t.Helper()
	return &sweepRig{tmp: t.TempDir(), alive: map[int]bool{}, now: time.Now()}
}

func (r *sweepRig) cluster(t *testing.T, name string, owner int, postmaster int, age time.Duration) string {
	t.Helper()
	root := filepath.Join(r.tmp, name)
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if owner > 0 {
		if err := os.WriteFile(filepath.Join(root, ownerFile), []byte(strconv.Itoa(owner)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if postmaster > 0 {
		if err := os.WriteFile(filepath.Join(root, "data", "postmaster.pid"), []byte(strconv.Itoa(postmaster)+"\n/some/dir\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := r.now.Add(-age)
	if err := os.Chtimes(root, old, old); err != nil {
		t.Fatal(err)
	}
	return root
}

func (r *sweepRig) sweep() int {
	return sweepStale(r.tmp, r.now, func(pid int) bool { return r.alive[pid] }, func(pid int, dir string) { r.killed = append(r.killed, pid); r.dirs = append(r.dirs, dir) })
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestSweepStopsAndRemovesAClusterWhoseOwnerIsDead(t *testing.T) {
	r := newSweepRig(t)
	dead := r.cluster(t, "ghost-embedded-pg-5001", 111, 222, time.Minute)
	if n := r.sweep(); n != 1 {
		t.Fatalf("swept %d, want 1", n)
	}
	if exists(dead) || len(r.killed) != 1 || r.killed[0] != 222 || r.dirs[0] != filepath.Join(dead, "data") {
		t.Fatalf("the orphaned postmaster 222 must be stopped and its directory removed: killed=%v exists=%v", r.killed, exists(dead))
	}
}

func TestSweepNeverTouchesAClusterWhoseOwnerIsStillRunning(t *testing.T) {
	r := newSweepRig(t)
	r.alive[111] = true
	live := r.cluster(t, "ghost-embedded-pg-5002", 111, 222, 5*time.Hour)
	if n := r.sweep(); n != 0 || !exists(live) || len(r.killed) != 0 {
		t.Fatalf("a live owner's cluster (a parallel test process) must survive: swept=%d killed=%v", n, r.killed)
	}
}

func TestSweepTreatsAnOwnerlessClusterAsStaleOnlyWhenOld(t *testing.T) {
	r := newSweepRig(t)
	young := r.cluster(t, "ghost-embedded-pg-5003", 0, 333, time.Minute)
	old := r.cluster(t, "ghost-embedded-pg-5004", 0, 444, 3*time.Hour)
	if n := r.sweep(); n != 1 || !exists(young) || exists(old) {
		t.Fatalf("swept=%d young kept=%v old kept=%v", n, exists(young), exists(old))
	}
	if len(r.killed) != 1 || r.killed[0] != 444 {
		t.Fatalf("killed = %v", r.killed)
	}
}

func TestSweepIgnoresOtherDirectoriesAndAClusterWithoutAPostmaster(t *testing.T) {
	r := newSweepRig(t)
	other := r.cluster(t, "something-else-5005", 111, 222, 10*time.Hour)
	bare := r.cluster(t, "ghost-embedded-pg-5006", 111, 0, time.Minute)
	if n := r.sweep(); n != 1 || !exists(other) || exists(bare) || len(r.killed) != 0 {
		t.Fatalf("swept=%d killed=%v", n, r.killed)
	}
	if got := sweepStale(filepath.Join(r.tmp, "missing"), r.now, nil, nil); got != 0 {
		t.Fatalf("a missing temp dir sweeps nothing: %d", got)
	}
}

func TestOnlyThePostmasterOfThatDataDirIsKillable(t *testing.T) {
	dir := `C:\Temp\ghost-embedded-pg-5001\data`
	cmd := `"C:/Temp/ghost-embedded-pg-5001/bin/bin/postgres.exe" -D "C:/Temp/ghost-embedded-pg-5001/data" -p 5001`
	for _, c := range []struct {
		name, cmd, dir string
		want           bool
	}{
		{"postgres.exe", cmd, dir, true},
		{"Postgres.EXE", cmd, dir, true},
		{"chrome.exe", cmd, dir, false},                                  // the pid was reused by an unrelated process
		{"postgres.exe", cmd, `C:\Temp\ghost-embedded-pg-9\data`, false}, // another cluster's postgres
		{"postgres.exe", cmd, "", false},
	} {
		if got := isClusterPostmaster(c.name, c.cmd, c.dir); got != c.want {
			t.Errorf("%s dir=%q: got %v want %v", c.name, c.dir, got, c.want)
		}
	}
}

func TestLaunchRecordsItsOwnerSoAParallelSweepSparesIt(t *testing.T) {
	root := t.TempDir()
	if err := writeOwner(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, ownerFile))
	if err != nil || string(b) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("owner = %q, %v", b, err)
	}
	if !processAlive(os.Getpid()) {
		t.Fatal("this process is alive")
	}
}
