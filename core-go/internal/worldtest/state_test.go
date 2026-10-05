package worldtest

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/worldfixture"
)

func reader(t *testing.T) *readmodel.Reader {
	t.Helper()
	r, err := readmodel.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// For every event Ek the state at T = occurred_at(Ek) is the version before Ek's, and no value, evidence
// ref or source id of Ek..E5 is in it (the changed stage never shows its After value).
func TestStateAtWorldTimeNeverShowsTheEventOrAnythingAfter(t *testing.T) {
	w := seed(t)
	ctx := context.Background()
	for k := 1; k <= 5; k++ {
		raw, err := reader(t).StateWorldAsOf(ctx, w.Account, w.At(k))
		if k == 1 {
			if !errors.Is(err, readmodel.ErrNoStateBefore) {
				t.Fatalf("T=E1: the state before the first event is not computed, got %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("T=E%d: %v", k, err)
		}
		body := string(raw)
		assertNoneOf(t, "state at E"+itoa(k), body, markersFrom(k)...)
		assertNoneOf(t, "state at E"+itoa(k), body, ids(w, k, 5)...)
		assertNoneOf(t, "state at E"+itoa(k), body, sourceEvents(w, k, 5)...)
		if got := versionOf(t, raw); got != k-1 {
			t.Errorf("T=E%d: want version %d, got %d", k, k-1, got)
		}
	}
}

// The value the world held before the change is what shows, not the After value.
func TestStateAtWorldTimeShowsTheBeforeValueOfAChangedProperty(t *testing.T) {
	w := seed(t)
	raw, err := reader(t).StateWorldAsOf(context.Background(), w.Account, w.At(3))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), worldfixture.StageDiscovery) {
		t.Fatalf("at E3 the stage is still %s: %.400s", worldfixture.StageDiscovery, raw)
	}
	raw, err = reader(t).StateWorldAsOf(context.Background(), w.Account, justAfter(w.At(3)))
	if err != nil || !strings.Contains(string(raw), worldfixture.StageNegotiation) {
		t.Fatalf("just after E3 the stage is %s: err=%v %.400s", worldfixture.StageNegotiation, err, raw)
	}
}

// as_of keeps its computed-at meaning: in this replay everything was computed in October, so a September
// world time finds nothing under as_of, while world_as_of answers.
func TestComputedAtReadsAreUnchanged(t *testing.T) {
	w := seed(t)
	ctx := context.Background()
	at := w.At(3)
	if _, err := reader(t).State(ctx, w.Account, &at); !errors.Is(err, readmodel.ErrNoState) {
		t.Fatalf("as_of=E3 world time is before every computation: %v", err)
	}
	if _, err := reader(t).StateWorldAsOf(ctx, w.Account, at); err != nil {
		t.Fatalf("world_as_of=E3: %v", err)
	}
	later := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	raw, err := reader(t).State(ctx, w.Account, &later)
	if err != nil || !strings.Contains(string(raw), worldfixture.StageClosedWon) {
		t.Fatalf("as_of far in the future is the newest version: %v", err)
	}
}

func TestStateAtWorldTimeErrors(t *testing.T) {
	w := seed(t)
	ctx := context.Background()
	if _, err := reader(t).StateWorldAsOf(ctx, "99999999-9999-4999-8999-999999999999", w.At(3)); !errors.Is(err, readmodel.ErrNotFound) {
		t.Errorf("unknown account: %v", err)
	}
	if _, err := reader(t).StateWorldAsOf(ctx, "not-a-uuid", w.At(3)); !errors.Is(err, readmodel.ErrNotFound) {
		t.Errorf("malformed id: %v", err)
	}
	if errors.Is(readmodel.ErrNoStateBefore, readmodel.ErrNoState) {
		t.Error("state_not_computed_before must be distinguishable from state_not_computed")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func versionOf(t *testing.T, raw []byte) int {
	t.Helper()
	var st struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	return st.Version
}
