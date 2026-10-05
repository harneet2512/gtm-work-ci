package demorun

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T, alive func(int) bool) PIDStore {
	t.Helper()
	return PIDStore{Dir: filepath.Join(t.TempDir(), "pids"), Alive: alive}
}

func TestPIDStoreWriteReadRemoveRoundTrip(t *testing.T) {
	s := newStore(t, func(int) bool { return true })
	rec := Record{Service: "core", PID: 4242, StartedAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), Log: "core.log"}
	if err := s.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := s.Read("core")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.PID != 4242 || got.Service != "core" || !got.StartedAt.Equal(rec.StartedAt) || got.Log != "core.log" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if err := s.Remove("core"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := s.Remove("core"); err != nil {
		t.Fatalf("Remove must be idempotent: %v", err)
	}
	if _, err := s.Read("core"); !errors.Is(err, ErrNotRecorded) {
		t.Fatalf("Read after Remove = %v, want ErrNotRecorded", err)
	}
}

func TestPIDStoreStateDistinguishesRunningStaleStoppedCorrupt(t *testing.T) {
	alive := map[int]bool{100: true, 200: false}
	s := newStore(t, func(pid int) bool { return alive[pid] })
	if st, _ := s.State("web"); st != StateStopped {
		t.Fatalf("no file: %v", st)
	}
	_ = s.Write(Record{Service: "web", PID: 100})
	if st, rec := s.State("web"); st != StateRunning || rec.PID != 100 {
		t.Fatalf("live pid: %v %+v", st, rec)
	}
	_ = s.Write(Record{Service: "worker", PID: 200})
	if st, _ := s.State("worker"); st != StateStale {
		t.Fatalf("dead pid must be stale: %v", st)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "core.pid.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.State("core"); st != StateCorrupt {
		t.Fatalf("garbage must be corrupt: %v", st)
	}
}

func TestPIDStoreRefusesToOverwriteALiveService(t *testing.T) {
	s := newStore(t, func(int) bool { return true })
	if err := s.Write(Record{Service: "core", PID: 1}); err != nil {
		t.Fatal(err)
	}
	err := s.Write(Record{Service: "core", PID: 2})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Write of a live service = %v, want ErrAlreadyRunning", err)
	}
	rec, _ := s.Read("core")
	if rec.PID != 1 {
		t.Fatal("the live record was overwritten")
	}
}

func TestPIDStoreOverwritesAStaleRecord(t *testing.T) {
	s := newStore(t, func(pid int) bool { return pid == 2 })
	_ = s.Write(Record{Service: "core", PID: 1}) // first write: nothing there
	if err := s.Write(Record{Service: "core", PID: 2}); err != nil {
		t.Fatalf("a stale record must be replaceable: %v", err)
	}
	if rec, _ := s.Read("core"); rec.PID != 2 {
		t.Fatalf("PID = %d", rec.PID)
	}
}

func TestPIDStoreRejectsBadInput(t *testing.T) {
	s := newStore(t, func(int) bool { return false })
	for _, bad := range []Record{{Service: "", PID: 1}, {Service: "../evil", PID: 1}, {Service: "a b", PID: 1}, {Service: "core", PID: 0}, {Service: "core", PID: -3}} {
		if err := s.Write(bad); err == nil {
			t.Errorf("Write(%+v) should fail", bad)
		}
	}
	if _, err := s.Read("../etc"); err == nil || errors.Is(err, ErrNotRecorded) {
		t.Errorf("Read of an invalid name must be a validation error, got %v", err)
	}
	if err := s.Remove("../etc"); err == nil {
		t.Error("Remove of an invalid name must fail")
	}
}

func TestPIDStoreListIsSortedAndIgnoresStrangers(t *testing.T) {
	s := newStore(t, func(int) bool { return true })
	_ = s.Write(Record{Service: "web", PID: 1})
	_ = s.Write(Record{Service: "core", PID: 2})
	if err := os.WriteFile(filepath.Join(s.Dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(s.List(), ",")
	if got != "core,web" {
		t.Fatalf("List = %q", got)
	}
}

func TestPIDStoreWriteLeavesNoTempFiles(t *testing.T) {
	s := newStore(t, func(int) bool { return false })
	_ = s.Write(Record{Service: "core", PID: 7})
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestProcessAliveSeesThisProcessAndNotAnImpossiblePID(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Fatal("this process must be alive")
	}
	if ProcessAlive(0) || ProcessAlive(-1) {
		t.Fatal("non-positive PIDs are never alive")
	}
}
