package coalesce_test

import (
	"context"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
)

func TestStateAtReadsStateHistoryAsOfATime(t *testing.T) {
	w := seedBasic(t)
	ctx := context.Background()
	ingestClock := clock.NewFixed(t0)
	svc := ingestService(t, ingestClock)
	coalClock := clock.NewFixed(t0.Add(time.Hour))
	s := newService(t, coalClock, coalesce.Options{Extractor: blockerExtractor()})

	// v1 computed at t0+1h knows the email of t0-3h; v2 computed at t0+25h also knows the email of t0+24h.
	ingestAll(t, svc, inbound(t, 1, t0.Add(-3*time.Hour), "First blocker. More"))
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	ingestClock.Set(t0.Add(24 * time.Hour))
	ingestAll(t, svc, inbound(t, 2, t0.Add(24*time.Hour), "Second blocker. More"))
	coalClock.Set(t0.Add(25 * time.Hour))
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	if _, found, err := coalesce.StateAt(ctx, env.DB, w.account, t0, coalesce.KnownAt); err != nil || found {
		t.Fatalf("before the first computation there is no state: found=%v err=%v", found, err)
	}
	for _, tc := range []struct {
		name    string
		at      time.Time
		basis   coalesce.Basis
		version int
	}{
		{"known at just after v1", t0.Add(2 * time.Hour), coalesce.KnownAt, 1},
		{"known at exactly v1", t0.Add(time.Hour), coalesce.KnownAt, 1},
		{"known between", t0.Add(24 * time.Hour), coalesce.KnownAt, 1},
		{"known at v2", t0.Add(25 * time.Hour), coalesce.KnownAt, 2},
		{"known long after", t0.Add(100 * time.Hour), coalesce.KnownAt, 2},
		{"world as of before the second email", t0.Add(12 * time.Hour), coalesce.WorldAsOf, 1},
		{"world as of the second email", t0.Add(24 * time.Hour), coalesce.WorldAsOf, 2},
	} {
		st, found, err := coalesce.StateAt(ctx, env.DB, w.account, tc.at, tc.basis)
		if err != nil || !found || st.Version != tc.version {
			t.Errorf("%s: version=%d found=%v err=%v, want %d", tc.name, st.Version, found, err, tc.version)
		}
	}
	if _, _, err := coalesce.StateAt(ctx, env.DB, w.account, t0, "sometime"); err == nil {
		t.Fatal("an unknown basis must be rejected")
	}
	if _, _, err := coalesce.StateAt(ctx, env.DB, "not-a-uuid", t0, coalesce.KnownAt); err == nil {
		t.Fatal("a bad account id must be an error")
	}
}
