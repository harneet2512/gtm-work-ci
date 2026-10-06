package knowledgestore

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// earn records one decision episode at t0+1h and one positive reaction at t0+2h: the narrow candidate becomes
// provisional (applicable) only once the second piece of evidence exists (lifecycle.v1.json).
func earn(t *testing.T, tx *sql.Tx, id string, base int, r knowledge.Rules) {
	t.Helper()
	for i, ev := range []knowledge.Evidence{
		{Kind: knowledge.EvidenceDecisionEpisode, RefID: uuid(base), At: t0.Add(time.Hour)},
		{Kind: knowledge.EvidenceCustomerReaction, RefID: uuid(base + 1), Polarity: "positive", At: t0.Add(2 * time.Hour)},
	} {
		if _, _, err := RecordEvidence(ctx, tx, id, ev, r); err != nil {
			t.Fatalf("evidence %d: %v", i, err)
		}
	}
}

func idsAt(t *testing.T, tx *sql.Tx, r knowledge.Rules, at time.Time) map[string]knowledge.Knowledge {
	t.Helper()
	got, err := ListApplicableAsOf(ctx, tx, r, at)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]knowledge.Knowledge{}
	for _, k := range got {
		out[k.ID] = k
	}
	return out
}

func TestListApplicableAsOfReplaysTheLifecycleToTheCursor(t *testing.T) {
	r := rules(t)
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		id := insert(t, tx, neutral(uuid(40))).ID
		earn(t, tx, id, 400, r)
		current, err := ListApplicable(ctx, tx, r)
		if err != nil || len(current) != 1 {
			t.Fatalf("today the knowledge is applicable: %v %v", current, err)
		}
		cases := []struct {
			name       string
			at         time.Time
			applicable bool
			decisions  int
		}{
			{"before it existed", t0.Add(-time.Hour), false, 0},
			{"created but unsupported", t0.Add(30 * time.Minute), false, 0},
			{"one decision, no reaction yet", t0.Add(90 * time.Minute), false, 1},
			{"earned at the reaction", t0.Add(2 * time.Hour), true, 1},
			{"long after", t0.Add(100 * time.Hour), true, 1},
		}
		for _, c := range cases {
			got, ok := idsAt(t, tx, r, c.at)[id]
			if ok != c.applicable {
				t.Fatalf("%s: applicable = %v, want %v", c.name, ok, c.applicable)
			}
			if ok && (got.Status != knowledge.StatusProvisional || got.Counts.Decisions != c.decisions) {
				t.Fatalf("%s: %+v", c.name, got)
			}
		}
	})
}

func TestKnowledgeLearnedAfterTheCursorNeverLeaks(t *testing.T) {
	r := rules(t)
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		early := insert(t, tx, neutral(uuid(41))).ID
		earn(t, tx, early, 410, r)
		lateK := neutral(uuid(42))
		lateK.CreatedAt = t0.Add(10 * time.Hour) // learned later, e.g. from the episode this run is replaying
		late := insert(t, tx, lateK).ID
		for i, ev := range []knowledge.Evidence{
			{Kind: knowledge.EvidenceDecisionEpisode, RefID: uuid(420), At: t0.Add(11 * time.Hour)},
			{Kind: knowledge.EvidenceCustomerReaction, RefID: uuid(421), Polarity: "positive", At: t0.Add(12 * time.Hour)},
		} {
			if _, _, err := RecordEvidence(ctx, tx, late, ev, r); err != nil {
				t.Fatalf("late evidence %d: %v", i, err)
			}
		}
		atCursor := idsAt(t, tx, r, t0.Add(5*time.Hour))
		if _, ok := atCursor[early]; !ok {
			t.Fatal("knowledge earned before the cursor must be offered")
		}
		if _, ok := atCursor[late]; ok {
			t.Fatal("knowledge created after the cursor leaked into the cursor's guidance")
		}
		if _, ok := idsAt(t, tx, r, t0.Add(13*time.Hour))[late]; !ok {
			t.Fatal("the same knowledge is offered once the cursor has passed it")
		}
	})
}

func TestAnEventTimeCursorIsRequired(t *testing.T) {
	if _, err := ListApplicableAsOf(ctx, env.DB, knowledge.Rules{}, time.Time{}); err == nil {
		t.Fatal("a zero cursor must be refused (it would mean the wall clock)")
	}
}

// A knowledge row's created_at is compared with the replay clock by ListApplicableAsOf, so a replay writer must
// give it in replay time: the database default (now()) is wall time and would hide the row from every replay
// cursor before today, or leak it to none.
func TestInsertReplayRequiresACreatedAtInReplayTime(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		k := neutral(uuid(9101))
		k.CreatedAt = time.Time{}
		if _, err := InsertReplay(ctx, tx, k, "replayed from a human delta"); !errors.Is(err, ErrCreatedAtRequired) {
			t.Fatalf("a replay insert without created_at = %v, want ErrCreatedAtRequired", err)
		}
		k.CreatedAt = t0.Add(-72 * time.Hour)
		got, err := InsertReplay(ctx, tx, k, "replayed from a human delta")
		if err != nil {
			t.Fatal(err)
		}
		if !got.CreatedAt.Equal(k.CreatedAt) {
			t.Fatalf("created_at = %s, want the replay time %s", got.CreatedAt, k.CreatedAt)
		}
	})
}

func TestLiveInsertStillTakesTheDatabaseClockWhenCreatedAtIsUnset(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		k := neutral(uuid(9102))
		k.CreatedAt = time.Time{}
		got, err := Insert(ctx, tx, k, "created live")
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(got.CreatedAt) > time.Minute || got.CreatedAt.Before(t0) {
			t.Fatalf("a live insert is stamped by the database clock, got %s", got.CreatedAt)
		}
	})
}
