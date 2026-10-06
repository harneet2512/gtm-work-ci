package pipeline_test

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/pipeline"
	"github.com/harneet2512/gtm-work/core-go/internal/runs"
	"github.com/harneet2512/gtm-work/core-go/internal/signalhistory"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

func idsOf(t *testing.T, at time.Time, account string) []string {
	t.Helper()
	open, err := signalstore.OpenSignalsAsOf(context.Background(), env.DB, account, "", at, coalesce.WorldAsOf)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, s := range open {
		out = append(out, s.Type)
	}
	sort.Strings(out)
	return out
}

// A signal fired in an EARLIER diff is still open later, within its window: the reason the HAR-118
// benchmark scored exception recall 0.000 on each diff's own signals.
func TestSignalFromAnEarlierDiffStaysOpenForItsWindow(t *testing.T) {
	w := seedWorld(t)
	s := newStack(t, runs.DryRun)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	// five days later an unrelated, non-material email arrives: its own diff emits no signals worth keeping
	later := t0.Add(5 * 24 * time.Hour)
	s.ingestClock.Set(later)
	ingestAll(t, s.svc, email(t, 2, later, "inbound", "Looking forward to it. Priya"))
	s.drain(t, later.Add(time.Minute))
	if count(t, "state_diffs") != "2" {
		t.Fatalf("state_diffs = %s", count(t, "state_diffs"))
	}

	at := later.Add(2 * time.Minute)
	if got := idsOf(t, at, w.account); !contains(got, "security_blocker_appeared") || !contains(got, "customer_replied") {
		t.Fatalf("signals open at %s = %v; the first email's signals must still be open", at, got)
	}
	// past the 14-day window the EVENT signals are gone; the STANDING one is left to the state
	far := t0.Add(30 * 24 * time.Hour)
	got := idsOf(t, far, w.account)
	if contains(got, "customer_replied") || !contains(got, "security_blocker_appeared") {
		t.Fatalf("at day 30: %v", got)
	}
	// nothing was open before anything happened
	if before := idsOf(t, t0.Add(-24*time.Hour), w.account); len(before) != 0 {
		t.Fatalf("before the first email: %v", before)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// The SQL query and the pure function (also used by the benchmark) agree at every instant.
func TestSQLQueryMatchesThePureOpenAsOf(t *testing.T) {
	w := seedWorld(t)
	s := newStack(t, runs.DryRun)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	all, err := signalstore.LoadRecords(context.Background(), env.DB, w.account, time.Time{}, coalesce.WorldAsOf)
	if err != nil || len(all) == 0 {
		t.Fatalf("records = %d, err = %v", len(all), err)
	}
	for _, d := range []time.Duration{-time.Hour, 0, time.Hour, 13 * 24 * time.Hour, 15 * 24 * time.Hour, 100 * 24 * time.Hour} {
		at := t0.Add(d)
		fromSQL, err := signalstore.OpenSignalsAsOf(context.Background(), env.DB, w.account, "", at, coalesce.WorldAsOf)
		if err != nil {
			t.Fatal(err)
		}
		pure := signalhistory.OpenAsOf(all, "", at)
		if len(fromSQL) != len(pure) {
			t.Fatalf("at %s: sql %d, pure %d", at, len(fromSQL), len(pure))
		}
		for i := range pure {
			if fromSQL[i].ID != pure[i].ID || fromSQL[i].Type != pure[i].Type || !fromSQL[i].CreatedAt.Equal(pure[i].CreatedAt) {
				t.Fatalf("at %s: %+v vs %+v", at, fromSQL[i], pure[i])
			}
		}
	}
}

// A blocker signal resolves its item by key, so a rewording does not close it and the situation carries it.
func TestSituationAtCarriesTheBlockerSubjectAndOpenSignals(t *testing.T) {
	w := seedWorld(t)
	s := newStack(t, runs.DryRun)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	sit, found, err := signalstore.SituationAt(context.Background(), env.DB, w.account, t0.Add(time.Hour), coalesce.KnownAt)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	var blocker bool
	for _, sig := range sit.Signals {
		if sig.Type == "security_blocker_appeared" {
			blocker = sig.SubjectClaimID != nil && sig.SubjectItemKey == "we need the soc2 report before signing"
		}
	}
	if !blocker {
		t.Fatalf("signals = %+v", sit.Signals)
	}
	if _, found, _ := signalstore.SituationAt(context.Background(), env.DB, w.account, t0.Add(-24*time.Hour), coalesce.KnownAt); found {
		t.Fatal("no state existed yet")
	}
}

func TestTickWritesSilenceOnceAndIsIdempotent(t *testing.T) {
	w := seedWorld(t)
	s := newStack(t, runs.DryRun)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	ctx := context.Background()
	if res, err := pipeline.Tick(ctx, env.DB, t0.Add(24*time.Hour)); err != nil || res.Signals != 0 {
		t.Fatalf("one quiet day is not silence: %+v %v", res, err)
	}
	tenDays := t0.Add(10 * 24 * time.Hour)
	if res, err := pipeline.Tick(ctx, env.DB, tenDays); err != nil || res.Accounts != 1 || res.Signals != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := pipeline.Tick(ctx, env.DB, tenDays.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, `SELECT count(*)::text FROM signals WHERE signal_type = 'customer_went_silent' AND account_id = $1::uuid AND state_diff_id IS NULL`, w.account); got != "1" {
		t.Fatalf("silence signals = %s, want exactly 1", got)
	}
	want := []string{"customer_replied", "customer_went_silent", "security_blocker_appeared", "stakeholder_gap"}
	if got := idsOf(t, tenDays.Add(48*time.Hour), w.account); !reflect.DeepEqual(got, want) {
		t.Fatalf("open at day 12 = %v, want %v", got, want)
	}
}

// Under KnownAt a signal recorded after T is not visible at T even though it occurred before T (a late
// transcript); under WorldAsOf it is. SituationAt picks the signal clock with the state clock.
func TestKnownAtDoesNotLeakLateRecordedSignals(t *testing.T) {
	w := seedWorld(t)
	s := newStack(t, runs.DryRun)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email)) // occurred t0-1h, recorded t0+1min
	s.drain(t, t0.Add(time.Minute))
	ctx := context.Background()
	between := t0.Add(-30 * time.Minute) // after it occurred, before we recorded it
	known, err := signalstore.OpenSignalsAsOf(ctx, env.DB, w.account, "", between, coalesce.KnownAt)
	if err != nil || len(known) != 0 {
		t.Fatalf("KnownAt must not see a signal recorded later: %d %v", len(known), err)
	}
	world, err := signalstore.OpenSignalsAsOf(ctx, env.DB, w.account, "", between, coalesce.WorldAsOf)
	if err != nil || len(world) == 0 {
		t.Fatalf("WorldAsOf sees it: %d %v", len(world), err)
	}
	after, err := signalstore.OpenSignalsAsOf(ctx, env.DB, w.account, "", t0.Add(2*time.Minute), coalesce.KnownAt)
	if err != nil || len(after) == 0 {
		t.Fatalf("once recorded it is visible under KnownAt: %d %v", len(after), err)
	}
	if _, err := signalstore.OpenSignalsAsOf(ctx, env.DB, w.account, "", time.Time{}, coalesce.KnownAt); err == nil {
		t.Fatal("a zero as-of time must be an error")
	}
}
