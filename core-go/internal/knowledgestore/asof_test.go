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

// narrowVerdictKnowledge is what a corrected judgment verdict seeds (learning.SeedVerdictCriterion): created_from manual,
// the source episode named, scoped by transition status, endpoint and stage.
func narrowVerdictKnowledge(id, episode string) knowledge.Knowledge {
	k := neutral(id)
	k.SituationSignature = []knowledge.Condition{{Field: "transition.status", Op: "eq", Value: "CANDIDATE"}}
	k.ApplicabilityConditions = []knowledge.Condition{{Field: "transition.to_state", Op: "eq", Value: "EXPANSION"},
		{Field: "stage", Op: "eq", Value: "Negotiation"}}
	k.Provenance = knowledge.Provenance{CreatedFrom: "manual", SourceDecisionEpisodeID: &episode}
	return k
}

// The central claim of HAR-129 (step 32): the corrected verdict of one case is retrieved by the next similar case. The
// seeded candidate is seeded from its own corrected, sent episode and has no other evidence and no customer reaction yet, so only the narrow-candidate rule
// can offer it; it is offered from the moment its episode supports it, never before, and a human_delta seed or a
// verdict with a broad scope stays out until a positive reaction.
func TestACorrectedVerdictCandidateIsRetrievedAsOfItsOwnEpisode(t *testing.T) {
	r := rules(t)
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		verdict := insert(t, tx, narrowVerdictKnowledge(uuid(60), uuid(600))).ID
		broad := narrowVerdictKnowledge(uuid(61), uuid(610))
		broad.ApplicabilityConditions = nil
		broadID := insert(t, tx, broad).ID
		delta := narrowVerdictKnowledge(uuid(62), uuid(620))
		delta.Provenance.CreatedFrom = "human_delta"
		deltaID := insert(t, tx, delta).ID
		for i, id := range []string{verdict, broadID, deltaID} {
			ev := knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: uuid(600 + 10*i), At: t0.Add(time.Hour)}
			if _, _, err := RecordEvidence(ctx, tx, id, ev, r); err != nil {
				t.Fatal(err)
			}
		}
		if _, ok := idsAt(t, tx, r, t0.Add(30*time.Minute))[verdict]; ok {
			t.Fatal("offered before its own episode supports it")
		}
		got := idsAt(t, tx, r, t0.Add(90*time.Minute))
		k, ok := got[verdict]
		if !ok || k.Status != knowledge.StatusCandidate {
			t.Fatalf("the corrected verdict's candidate must be retrieved while still a candidate: %v %+v", ok, k)
		}
		// Offered by similarity (ADR-0013 amendment 2) the broad scope is bounded by the similarity threshold and its
		// minimum comparable features at match time; under exact matching it would stay out.
		if _, ok := got[broadID]; !ok {
			t.Fatal("a broad-scoped corrected verdict is offered for scoring: similarity bounds it, not the scope breadth")
		}
		exact := r
		exact.Similarity = nil
		if _, ok := idsAt(t, tx, exact, t0.Add(90*time.Minute))[broadID]; ok {
			t.Fatal("under exact matching a broad-scoped candidate must not be offered")
		}
		if _, ok := got[deltaID]; ok {
			t.Fatal("a human_delta candidate still needs its positive reaction")
		}
		current, err := ListApplicable(ctx, tx, r)
		if err != nil {
			t.Fatal(err)
		}
		if len(current) != 2 {
			t.Fatalf("the current listing must agree with the as-of one: %v", current)
		}
	})
}

// Only the latest correction per episode is offered: the human who corrects a verdict again replaces the first
// correction, which must stop being offered from the moment the second exists (and not before).
func TestASupersededCorrectionIsNotOffered(t *testing.T) {
	r := rules(t)
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		episode, other := uuid(700), uuid(710)
		first := narrowVerdictKnowledge(uuid(70), episode)
		second := narrowVerdictKnowledge(uuid(71), episode)
		second.CreatedAt = t0.Add(10 * time.Minute)
		unrelated := narrowVerdictKnowledge(uuid(72), other)
		unrelated.CreatedAt = t0.Add(20 * time.Minute)
		ids := []string{insert(t, tx, first).ID, insert(t, tx, second).ID, insert(t, tx, unrelated).ID}
		for i, id := range ids {
			ev := knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: []string{episode, episode, other}[i], At: t0.Add(time.Minute)}
			if _, _, err := RecordEvidence(ctx, tx, id, ev, r); err != nil {
				t.Fatal(err)
			}
		}
		mid := idsAt(t, tx, r, t0.Add(5*time.Minute))
		if _, ok := mid[ids[0]]; !ok {
			t.Fatal("a lone correction is offered")
		}
		later := idsAt(t, tx, r, t0.Add(3*time.Hour))
		if _, ok := later[ids[0]]; ok {
			t.Fatal("the replaced correction must not be offered")
		}
		if _, ok := later[ids[1]]; !ok {
			t.Fatal("the latest correction is offered")
		}
		if _, ok := later[ids[2]]; !ok {
			t.Fatal("a correction of another episode is unaffected")
		}
		now, err := ListApplicable(ctx, tx, r)
		if err != nil || len(now) != 2 {
			t.Fatalf("the current listing agrees: %d %v", len(now), err)
		}
	})
}

// Retiring the criterion a correction came with withdraws the knowledge's offer.
func TestKnowledgeOfARetiredCriterionIsNotOffered(t *testing.T) {
	r := rules(t)
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		id := insert(t, tx, narrowVerdictKnowledge(uuid(80), uuid(800))).ID
		ev := knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: uuid(800), At: t0.Add(time.Hour)}
		if _, _, err := RecordEvidence(ctx, tx, id, ev, r); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO evaluator_versions (evaluator, version, status, kind, rubric, created_from, knowledge_id)
 VALUES ('human_delta', 9001, 'candidate', 'human_delta', 'r', 'human_delta', $1::uuid)`, id); err != nil {
			t.Fatal(err)
		}
		at := t0.Add(2 * time.Hour)
		if _, ok := idsAt(t, tx, r, at)[id]; !ok {
			t.Fatal("a live candidate criterion keeps the offer")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE evaluator_versions SET status = 'retired' WHERE version = 9001`); err != nil {
			t.Fatal(err)
		}
		if _, ok := idsAt(t, tx, r, at)[id]; ok {
			t.Fatal("a retired criterion's knowledge must not be offered")
		}
		if now, err := ListApplicable(ctx, tx, r); err != nil || len(now) != 0 {
			t.Fatalf("the current listing agrees: %d %v", len(now), err)
		}
	})
}
