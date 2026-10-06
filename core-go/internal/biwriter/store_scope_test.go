package biwriter_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/biwriter"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
	"github.com/harneet2512/gtm-work/core-go/internal/transitionstore"
)

// An open transition the released event did not touch is read from the account and labelled unchanged.
func TestAnOpenTransitionTheEventDidNotTouchIsRestatedAsUnchanged(t *testing.T) {
	r := releaseEvent(t, replaytest.NewHeldOut(2), true)
	to := "EXPANSION"
	older := replaytest.One(t, env.DB, `SELECT id::text FROM activities WHERE id <> $1::uuid AND account_id = $2::uuid ORDER BY occurred_at LIMIT 1`, r.src.ActivityID, r.world.Account)
	rec := transitions.Record{AccountID: r.world.Account, FromState: "REORG", ToStateCandidate: &to, Status: transitions.StatusCandidate,
		TriggerActivityIDs: []string{older}, ContradictingFacts: []transitions.FactResult{}, Confidence: 0.4,
		SupportingFacts: []transitions.FactResult{{Key: "expansion_need_stated", Description: "A broader rollout was asked for.", Required: true, Satisfied: true,
			EvidenceRefs: []transitions.Ref{{ActivityID: older, Quote: "an earlier email"}}, SignalIDs: []string{}}},
		MissingFacts:   []transitions.FactResult{{Key: "owner_stabilized", Description: "A champion is stable.", Required: true, EvidenceRefs: []transitions.Ref{}, SignalIDs: []string{}}},
		RuleSetVersion: "transition_rules:v1", FirstObservedAt: replaytest.T0, LastUpdatedAt: replaytest.T0}
	if err := env.DB.QueryRow(`SELECT version FROM account_state WHERE account_id = $1::uuid`, r.world.Account).Scan(&rec.StateVersion); err != nil {
		t.Fatal(err)
	}
	stored, err := transitionstore.Save(bg, env.DB, rec)
	if err != nil {
		t.Fatalf("save transition: %v", err)
	}
	f, err := biwriter.LoadFacts(bg, env.DB, r.src)
	if err != nil {
		t.Fatal(err)
	}
	if f.Transition == nil || f.Transition.ID != stored.ID || f.Transition.Touched {
		t.Fatalf("transition fact = %+v, want the open transition, not touched", f.Transition)
	}
	_, st := writeResult(t, f)
	_, update, err := biwriter.Read(bg, env.DB, st.ChangeID)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := schemacheck.New()
	if err := v.Validate("business_intelligence_update", update); err != nil {
		t.Fatalf("%v\n%s", err, update)
	}
	if !strings.Contains(spaced(update), `"touched_by_event": false`) || !strings.Contains(spaced(update), `"owner_stabilized"`) {
		t.Errorf("the update does not restate the open transition as unchanged:\n%s", update)
	}
}

// Job ids are one global counter: the graph diff ref starts at the account's own previous job, not at "first
// job minus one", which can be another account's job.
func TestGraphDiffRefIsScopedToTheAccount(t *testing.T) {
	w := replaytest.SeedWorld(t, env.DB)
	stack := replaytest.NewStack(t, env.DB)
	stack.History(t, 2)
	other := replaytest.One(t, env.DB, `INSERT INTO accounts (name, domain) VALUES ('Other Co', 'other.example') RETURNING id::text`)
	for i := 0; i < 3; i++ { // other accounts' jobs between the history and the event
		if _, err := env.DB.Exec(`INSERT INTO graph_projection_jobs (account_id, claimed_at, claimed_by, lease_expires_at, completed_at)
			VALUES ($1::uuid, now(), 'x', now(), now())`, other); err != nil {
			t.Fatal(err)
		}
	}
	ev := replaytest.NewHeldOut(2)
	res, err := stack.Ingest.Ingest(bg, ev.Event)
	if err != nil {
		t.Fatal(err)
	}
	if err := stack.Drain(bg); err != nil {
		t.Fatal(err)
	}
	replaytest.FakeProjector{DB: env.DB}.Complete(t)
	src := biwriter.Source{ActivityID: res.ActivityID, SourceEventID: res.SourceEventID, OpportunityID: w.Opportunity, HeldOutEventID: ev.EventID}
	f, err := biwriter.LoadFacts(bg, env.DB, src)
	if err != nil {
		t.Fatal(err)
	}
	first := f.Graph.JobIDs[0]
	for _, id := range f.Graph.JobIDs {
		first = min(first, id)
	}
	want := replaytest.One(t, env.DB, `SELECT max(id)::text FROM graph_projection_jobs WHERE account_id = $1::uuid AND id < $2`, w.Account, first)
	if got := strconv.FormatInt(f.Graph.PrevJobID, 10); got != want {
		t.Fatalf("PrevJobID = %s, want the account's previous job %s", got, want)
	}
	if f.Graph.PrevJobID >= first-1 {
		t.Fatalf("setup: other accounts' jobs must sit between (prev %d, first %d)", f.Graph.PrevJobID, first)
	}
	r, _ := writeResult(t, f)
	if got := r.Change.GraphDiffRef.FromProjectionSeq; got != f.Graph.PrevJobID {
		t.Fatalf("from_projection_seq = %d, want %d", got, f.Graph.PrevJobID)
	}
}
