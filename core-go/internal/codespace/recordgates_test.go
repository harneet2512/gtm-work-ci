package codespace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGates persists the gates of an episode one poll at a time, like background goroutines finishing.
type fakeGates struct {
	polls   int
	partial int      // polls that still answer only the first gates
	have    []string // what a complete episode holds
	never   []string // gates that are never persisted
}

func (g *fakeGates) Persisted(_ context.Context, _ Case, _ string) ([]string, error) {
	g.polls++
	out := []string{}
	for _, name := range g.have {
		skip := false
		for _, n := range g.never {
			skip = skip || n == name
		}
		if !skip {
			out = append(out, name)
		}
	}
	if g.polls <= g.partial {
		return out[:3], nil
	}
	return out, nil
}

func TestExpectedGatesAreB1ToB9AndD1ToD10(t *testing.T) {
	got := ExpectedGates()
	if len(got) != 19 || got[0] != "B1" || got[8] != "B9" || got[9] != "D1" || got[18] != "D10" {
		t.Fatalf("ExpectedGates = %v", got)
	}
}

func TestRecordWaitsForEveryGateRowBeforeMovingOn(t *testing.T) {
	rr := newRecorder(t)
	gates := &fakeGates{partial: 4, have: ExpectedGates()}
	rr.rec.Gates = gates
	if err := rr.rec.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gates.polls != 4+2 { // 4 partial polls, then one complete poll in each of the two cases
		t.Fatalf("the recorder stopped waiting after %d polls, before every gate row was there", gates.polls)
	}
}

func TestRecordFailsNamingTheGatesThatNeverAppeared(t *testing.T) {
	rr := newRecorder(t)
	rr.rec.Gates = &fakeGates{have: ExpectedGates(), never: []string{"D8", "B3"}}
	rr.rec.Wait, rr.rec.Poll = 3*time.Second, time.Second
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing: B3, D8") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordWaitsUntilTheCacheCallCountSettles(t *testing.T) {
	rr := newRecorder(t)
	rr.rec.Gates = &fakeGates{have: ExpectedGates()}
	calls := 0
	rr.rec.CacheCalls = func() (int, error) {
		calls++
		if calls < 6 {
			return calls, nil // a background gate is still calling the model
		}
		return 6, nil
	}
	if err := rr.rec.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls < 8 {
		t.Fatalf("stopped polling after %d reads while the count was still moving", calls)
	}
}

func TestStrictRunFailsOnAnyCacheMissIncludingBackgroundGateCalls(t *testing.T) {
	rr := newRecorder(t)
	misses := 0
	rr.rec.Misses = func() (int, error) { return misses, nil }
	// the miss lands while a gate runs in the background, after the human acted
	rr.rec.Gates = &missingOnPoll{inner: &fakeGates{have: ExpectedGates()}, onPoll: func() { misses = 2 }}
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refused 2 request(s) with no recording") {
		t.Fatalf("a strict miss must fail the run: %v", err)
	}
	if active, _ := ReadMarker(rr.rig.ops.Paths.ActiveFile()); active != SlotCase1 {
		t.Fatalf("a failed record must still leave the chronology reset: %q", active)
	}
}

type missingOnPoll struct {
	inner  GateProbe
	onPoll func()
}

func (m *missingOnPoll) Persisted(ctx context.Context, c Case, e string) ([]string, error) {
	m.onPoll()
	return m.inner.Persisted(ctx, c, e)
}

func TestMissesThatPredateTheRunDoNotFailIt(t *testing.T) {
	rr := newRecorder(t)
	rr.rec.Misses = func() (int, error) { return 7, nil }
	if err := rr.rec.Run(context.Background()); err != nil {
		t.Fatalf("an old miss is not this run's: %v", err)
	}
}

func TestReadCacheCountersReadsTheWorkersStatsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cache-stats.json"), []byte(`{"hits":3,"recorded":2,"misses":4}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := readCacheCountersAt(filepath.Join(dir, "cache-stats.json"))
	if err != nil || c.Calls() != 9 || c.Misses != 4 {
		t.Fatalf("counters = %+v, %v", c, err)
	}
	if c, err = readCacheCountersAt(filepath.Join(dir, "none.json")); err != nil || c.Calls() != 0 {
		t.Fatalf("a missing file is an unused cache: %+v, %v", c, err)
	}
}
