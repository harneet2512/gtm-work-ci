package codespace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// writeTree writes files (relative slash path -> content) under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func newBaseline(t *testing.T) (Baseline, string) {
	t.Helper()
	home := t.TempDir()
	writeTree(t, home, map[string]string{
		"live/pg/data/base/1/1": "rows-v1", "live/pg/data/PG_VERSION": "16",
		"live/neo4j/data/db": "graph-1", "live/cases/case1/state.json": `{"a":1}`, "live/active-case": "case1\n",
		"cassettes/k1.json": "model-answer",
	})
	b := Baseline{Home: home, Paths: []string{"live/pg/data", "live/neo4j/data", "live/cases", "live/active-case"},
		Now: func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }}
	return b, home
}

func fingerprintOf(t *testing.T, path string) treePrint {
	t.Helper()
	p, err := fingerprint(path, false)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func seal(t *testing.T, b Baseline) {
	t.Helper()
	if _, err := b.Seal(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSealWritesManifestWithEveryComponent(t *testing.T) {
	b, _ := newBaseline(t)
	m, err := b.Seal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !m.BuiltAt.Equal(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)) || m.Version != BaselineVersion {
		t.Fatalf("manifest header = %+v", m)
	}
	if len(m.Components) != 4 {
		t.Fatalf("components = %d, want 4", len(m.Components))
	}
	pg := m.Component("live/pg/data")
	if pg == nil || pg.Files != 2 || pg.Bytes != int64(len("rows-v1")+len("16")) || pg.Content == "" || pg.Layout == "" {
		t.Fatalf("pg component = %+v", pg)
	}
	if err := b.Verify(); err != nil {
		t.Fatalf("a freshly sealed baseline must verify: %v", err)
	}
	if err := b.VerifyDeep(); err != nil {
		t.Fatalf("deep verify: %v", err)
	}
}

func TestSealRefusesWhilePostgresIsRunning(t *testing.T) {
	b, home := newBaseline(t)
	writeTree(t, home, map[string]string{"live/pg/data/postmaster.pid": "123"})
	if _, err := b.Seal(context.Background()); err == nil || !strings.Contains(err.Error(), "postmaster.pid") {
		t.Fatalf("err = %v, want a refusal naming postmaster.pid", err)
	}
}

func TestSealRefusesAMissingComponent(t *testing.T) {
	b, home := newBaseline(t)
	_ = os.RemoveAll(filepath.Join(home, "live", "neo4j"))
	if _, err := b.Seal(context.Background()); err == nil || !strings.Contains(err.Error(), "live/neo4j/data") {
		t.Fatalf("err = %v, want the missing component named", err)
	}
}

func TestVerifyMissingBaselineTellsTheOperatorToRunSetup(t *testing.T) {
	b, _ := newBaseline(t)
	err := b.Verify()
	if !errors.Is(err, ErrNoBaseline) || !strings.Contains(err.Error(), "one-time setup") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	cases := map[string]func(t *testing.T, base string){
		"truncated file": func(t *testing.T, base string) {
			_ = os.WriteFile(filepath.Join(base, "live/pg/data/base/1/1"), []byte("ro"), 0o644)
		},
		"deleted file": func(t *testing.T, base string) { _ = os.Remove(filepath.Join(base, "live/pg/data/PG_VERSION")) },
		"extra file":   func(t *testing.T, base string) { writeTree(t, base, map[string]string{"live/pg/data/stray": "x"}) },
		"missing dir":  func(t *testing.T, base string) { _ = os.RemoveAll(filepath.Join(base, "live/cases")) },
		"bad manifest": func(t *testing.T, base string) {
			_ = os.WriteFile(filepath.Join(base, "manifest.json"), []byte("{nope"), 0o644)
		},
		"wrong version": func(t *testing.T, base string) {
			_ = os.WriteFile(filepath.Join(base, "manifest.json"), []byte(`{"version":"x"}`), 0o644)
		},
		"manifest absent": func(t *testing.T, base string) { _ = os.Remove(filepath.Join(base, "manifest.json")) },
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			b, _ := newBaseline(t)
			seal(t, b)
			corrupt(t, b.Dir())
			err := b.Verify()
			if err == nil || !strings.Contains(err.Error(), "one-time setup") {
				t.Fatalf("err = %v, want a message that points to the one-time setup", err)
			}
		})
	}
}

func TestVerifyDeepCatchesSameSizeCorruption(t *testing.T) {
	b, _ := newBaseline(t)
	seal(t, b)
	_ = os.WriteFile(filepath.Join(b.Dir(), "live/pg/data/base/1/1"), []byte("rows-v9"), 0o644) // same size
	if err := b.Verify(); err != nil {
		t.Fatalf("the fast check is size based: %v", err)
	}
	if err := b.VerifyDeep(); err == nil {
		t.Fatal("deep verify must catch a same-size change")
	}
}

func TestRestoreReturnsTheLiveStateToTheBaselineAndKeepsTheCache(t *testing.T) {
	b, home := newBaseline(t)
	seal(t, b)
	want := fingerprintOf(t, filepath.Join(home, "live"))
	// the demo moves the live state forward: edit, add, delete, and the cache grows
	writeTree(t, home, map[string]string{"live/pg/data/base/1/1": "rows-v2-after-play", "live/pg/data/base/1/new": "n",
		"live/neo4j/data/db": "graph-2", "live/active-case": "case2\n", "cassettes/k2.json": "new-answer"})
	_ = os.Remove(filepath.Join(home, "live/pg/data/PG_VERSION"))
	cacheBefore := fingerprintOf(t, filepath.Join(home, "cassettes"))

	stats, err := b.Restore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := fingerprintOf(t, filepath.Join(home, "live")); got != want {
		t.Fatalf("live after restore = %+v, want the baseline %+v", got, want)
	}
	if got := fingerprintOf(t, filepath.Join(home, "cassettes")); got != cacheBefore || got.Files != 2 {
		t.Fatalf("the model-call cache must survive a restore untouched: %+v", got)
	}
	if stats.Files != 5 || stats.Bytes == 0 {
		t.Fatalf("stats = %+v", stats)
	}
	for _, leftover := range []string{"live/pg/data.restore", "live/pg/data.old"} {
		if _, err := os.Stat(filepath.Join(home, leftover)); err == nil {
			t.Fatalf("%s was left behind", leftover)
		}
	}
	if err := b.Verify(); err != nil {
		t.Fatalf("the baseline itself must be unchanged by a restore: %v", err)
	}
}

func TestRestoreRebuildsMissingLiveState(t *testing.T) {
	b, home := newBaseline(t)
	seal(t, b)
	_ = os.RemoveAll(filepath.Join(home, "live"))
	if _, err := b.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "live/active-case")); string(got) != "case1\n" {
		t.Fatalf("active-case = %q", got)
	}
}

func TestRestoreWithACorruptBaselineLeavesLiveUntouched(t *testing.T) {
	b, home := newBaseline(t)
	seal(t, b)
	_ = os.Remove(filepath.Join(b.Dir(), "live/pg/data/PG_VERSION"))
	writeTree(t, home, map[string]string{"live/active-case": "case2\n"})
	if _, err := b.Restore(context.Background()); err == nil || !strings.Contains(err.Error(), "one-time setup") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "live/active-case")); string(got) != "case2\n" {
		t.Fatal("a failed restore must not touch the live state")
	}
}

func TestRestoreRetriesWindowsFileLocks(t *testing.T) {
	b, home := newBaseline(t)
	seal(t, b)
	writeTree(t, home, map[string]string{"live/active-case": "case2\n"})
	var failures atomic.Int32 // the components are restored side by side
	b.rename = func(from, to string) error {
		if strings.HasSuffix(from, "pg"+string(filepath.Separator)+"data") && failures.Add(1) <= 3 {
			return errors.New("The process cannot access the file because it is being used by another process")
		}
		return os.Rename(from, to)
	}
	b.Backoff = time.Millisecond
	if _, err := b.Restore(context.Background()); err != nil {
		t.Fatalf("a lock that clears must be retried: %v", err)
	}
	if got := failures.Load(); got != 4 { // three refusals, then the swap that worked
		t.Fatalf("rename attempts on the cluster = %d", got)
	}
}

func TestRestoreGivesUpAfterTheRetriesAndKeepsTheOldState(t *testing.T) {
	b, home := newBaseline(t)
	seal(t, b)
	b.rename = func(string, string) error { return errors.New("locked") }
	b.Backoff, b.Retries = time.Millisecond, 3
	if _, err := b.Restore(context.Background()); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "live/pg/data/PG_VERSION")); err != nil {
		t.Fatalf("the live state must survive a failed swap: %v", err)
	}
}

func TestRestoreHonoursCancellation(t *testing.T) {
	b, _ := newBaseline(t)
	seal(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Restore(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestIncludeAddsTheRunSheetAndRestoreBringsItBack(t *testing.T) {
	b, home := newBaseline(t)
	seal(t, b)
	writeTree(t, home, map[string]string{"RUNSHEET.txt": "press Play"})
	if err := b.Include(context.Background(), "RUNSHEET.txt"); err != nil {
		t.Fatal(err)
	}
	if err := b.Verify(); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, "RUNSHEET.txt"), []byte("edited"), 0o644)
	if _, err := b.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "RUNSHEET.txt")); string(got) != "press Play" {
		t.Fatalf("run sheet = %q", got)
	}
}

func TestIncludeMissingSourceFails(t *testing.T) {
	b, _ := newBaseline(t)
	seal(t, b)
	if err := b.Include(context.Background(), "RUNSHEET.txt"); err == nil {
		t.Fatal("a missing run sheet must fail")
	}
}

func TestNoteCacheRecordsSizeButIsNeverEnforced(t *testing.T) {
	b, home := newBaseline(t)
	seal(t, b)
	if err := b.NoteCache("cassettes"); err != nil {
		t.Fatal(err)
	}
	m, err := b.Manifest()
	if err != nil || m.Cache == nil || m.Cache.Files != 1 || m.Cache.Path != "cassettes" {
		t.Fatalf("cache note = %+v err=%v", m.Cache, err)
	}
	writeTree(t, home, map[string]string{"cassettes/more.json": "grown"})
	if err := b.Verify(); err != nil {
		t.Fatalf("a growing cache must not invalidate the baseline: %v", err)
	}
}

func TestRestorePutsThePreviousStateBackWhenTheNewOneCannotBeMovedIn(t *testing.T) {
	b, home := newBaseline(t)
	seal(t, b)
	writeTree(t, home, map[string]string{"live/pg/data/base/1/1": "rows-after-play"})
	before := fingerprintOf(t, filepath.Join(home, "live"))
	b.rename = func(from, to string) error {
		if strings.HasSuffix(from, ".restore") {
			return errors.New("locked")
		}
		return os.Rename(from, to)
	}
	b.Backoff, b.Retries = time.Millisecond, 2
	if _, err := b.Restore(context.Background()); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("err = %v", err)
	}
	if got := fingerprintOf(t, filepath.Join(home, "live")); got != before {
		t.Fatalf("the previous live state must be put back: %+v vs %+v", got, before)
	}
	for _, leftover := range []string{"live/pg/data.restore", "live/pg/data.old"} {
		if _, err := os.Stat(filepath.Join(home, leftover)); err == nil {
			t.Fatalf("%s was left behind", leftover)
		}
	}
}

func TestRestoreClearsLeftoversOfACrashedEarlierRestore(t *testing.T) {
	b, home := newBaseline(t)
	seal(t, b)
	writeTree(t, home, map[string]string{"live/pg/data.restore/junk": "x", "live/pg/data.old/junk": "x", "live/active-case.old": "x"})
	if _, err := b.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, leftover := range []string{"live/pg/data.restore", "live/pg/data.old", "live/active-case.old"} {
		if _, err := os.Stat(filepath.Join(home, leftover)); err == nil {
			t.Fatalf("%s survived a restore", leftover)
		}
	}
}

func TestSealRefusesPathsOutsideTheDemoHome(t *testing.T) {
	b, _ := newBaseline(t)
	for _, bad := range []string{"", "../outside", "/abs/path", "live/../x", `C:\x`} {
		b.Paths = []string{bad}
		if _, err := b.Seal(context.Background()); err == nil {
			t.Errorf("Seal accepted %q", bad)
		}
	}
	b.Paths = nil
	if _, err := b.Seal(context.Background()); err == nil {
		t.Error("sealing nothing must fail")
	}
}

func TestSealRefusesAnInconsistentCaseAndLeavesNoBaseline(t *testing.T) {
	b, home := newBaseline(t)
	b.Cases = []string{"case1"}
	writeTree(t, home, map[string]string{"live/cases/case1/manifest.json": `{"events":[],"held_out_event":{}}`})
	if _, err := b.Seal(context.Background()); err == nil || !strings.Contains(err.Error(), "case1") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(b.Dir()); err == nil {
		t.Fatal("a failed seal must not leave a baseline")
	}
	if _, err := os.Stat(b.Dir() + ".tmp"); err == nil {
		t.Fatal("a failed seal must not leave its staging directory")
	}
	writeTree(t, home, map[string]string{"live/cases/case1/manifest.json": manifestJSON(2, "h1"), "live/cases/case1/state.json": `{"held_out_event_id":"h1"}`})
	if _, err := b.Seal(context.Background()); err != nil {
		t.Fatalf("a consistent case seals: %v", err)
	}
	m, _ := b.Manifest()
	if len(m.History) != 1 || m.History[0].HistoryEvents != 2 {
		t.Fatalf("history = %+v", m.History)
	}
}

func TestRetryDefaultsAndGivingUp(t *testing.T) {
	var b Baseline
	if b.retries() != 10 || b.backoff() != 300*time.Millisecond {
		t.Fatalf("defaults = %d %s", b.retries(), b.backoff())
	}
	b.Retries, b.Backoff = 2, time.Millisecond
	calls := 0
	err := b.retry(context.Background(), func() error { calls++; return errors.New("busy") })
	if err == nil || calls != 2 || !strings.Contains(err.Error(), "after 2 attempts") {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestSealReplacesAnEarlierBaselineAtomically(t *testing.T) {
	b, home := newBaseline(t)
	seal(t, b)
	writeTree(t, home, map[string]string{"live/pg/data/base/1/1": "rows-v3"})
	seal(t, b)
	if got, _ := os.ReadFile(filepath.Join(b.Dir(), "live/pg/data/base/1/1")); string(got) != "rows-v3" {
		t.Fatalf("sealed content = %q", got)
	}
	if _, err := os.Stat(b.Dir() + ".tmp"); err == nil {
		t.Fatal("temporary baseline left behind")
	}
}
