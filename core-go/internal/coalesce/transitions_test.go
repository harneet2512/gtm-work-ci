package coalesce_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
	"github.com/harneet2512/gtm-work/core-go/internal/transitionstore"
)

// reorgWorld is an account whose relationship state is earned claim by claim (ADR-0012). Claims are written
// directly: the extractor does not emit org_change, expansion_need or business_value yet (extract-v5).
type reorgWorld struct {
	account, priya, marco, owen string
	n                           int
	detector                    bool
	clk                         *clock.Fixed
	svc                         *coalesce.Service
}

func newReorgWorld(t *testing.T, detector bool) *reorgWorld {
	t.Helper()
	resetAll(t)
	w := &reorgWorld{account: newAccount(t, "Acme Corp"), clk: clock.NewFixed(t0), detector: detector}
	person := func(name string) string {
		return scalar(t, `INSERT INTO people (kind, display_name, account_id) VALUES ('contact', $1, $2::uuid) RETURNING id::text`, name, w.account)
	}
	w.priya, w.marco, w.owen = person("Priya Shah"), person("Marco Ruiz"), person("Owen Clarke")
	opts := coalesce.Options{WorkerID: "w1"}
	if detector {
		rules, err := transitions.LoadRules(filepath.Join("..", "..", "..", "contracts", "transitions", "rules.v1.json"))
		if err != nil {
			t.Fatal(err)
		}
		opts.Detector = transitionstore.NewDetector(rules)
	}
	w.svc = newService(t, w.clk, opts)
	return w
}

// claim stores one claim of the account at the given time, citing a fresh activity.
func (w *reorgWorld) claim(t *testing.T, path claims.FieldPath, value, subject string, at time.Time, standing claims.Standing) string {
	t.Helper()
	w.n++
	act := newActivity(t, w.account, w.n, at)
	c := claims.Claim{AccountID: w.account, FieldPath: path, Value: json.RawMessage(value), SubjectPersonID: subject, Standing: standing,
		Confidence: 0.9, SourceActivityID: act, EvidenceQuote: "a verbatim quote", OccurredAt: at, Extractor: "test@1", Status: claims.StatusActive}
	if _, err := claimstore.InsertClaims(context.Background(), env.DB, []claims.Claim{c}); err != nil {
		t.Fatal(err)
	}
	return act
}

// recompute runs one recompute of the account at the clock time now, triggered by the given activity.
func (w *reorgWorld) recompute(t *testing.T, now time.Time, trigger string) {
	t.Helper()
	w.clk.Set(now)
	insertJob(t, w.account, now, []string{trigger}, "", time.Time{})
	res, err := w.svc.Drain(context.Background())
	if err != nil || res.Failed != 0 || len(res.Recomputes) != 1 {
		t.Fatalf("recompute at %s: %+v %v", now, res, err)
	}
}

func (w *reorgWorld) person(id string) string { return fmt.Sprintf(`"%s"`, id) }

func (w *reorgWorld) transitions(t *testing.T) []transitions.Record {
	t.Helper()
	rows, err := transitionstore.List(context.Background(), env.DB, w.account, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

type exposure struct {
	RelationshipState struct {
		Value        string  `json:"value"`
		TransitionID *string `json:"transition_id"`
		ConfirmedAt  *string `json:"confirmed_at"`
	} `json:"relationship_state"`
	OpenTransition *struct {
		TransitionID     string  `json:"transition_id"`
		FromState        string  `json:"from_state"`
		ToStateCandidate *string `json:"to_state_candidate"`
		Status           string  `json:"status"`
		MissingFacts     []struct {
			Key      string `json:"key"`
			Required bool   `json:"required"`
		} `json:"missing_facts"`
	} `json:"open_transition"`
}

// exposed reads the account's current state and checks it (and every stored version) against the contract.
func (w *reorgWorld) exposed(t *testing.T) exposure {
	t.Helper()
	raw := scalar(t, `SELECT state::text FROM account_state WHERE account_id = $1::uuid`, w.account)
	validate(t, raw)
	var e exposure
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func (w *reorgWorld) checkTransitionContract(t *testing.T) {
	t.Helper()
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range w.transitions(t) {
		raw, _ := json.Marshal(r)
		if err := v.Validate("state_transition", raw); err != nil {
			t.Errorf("transition %s violates state_transition.v1.json: %v\n%s", r.ID, err, raw)
		}
	}
}

func day(n int) time.Time { return t0.AddDate(0, 0, n) }

// earnReorg gives the account a stated org change about its champion, which confirms REORG.
func (w *reorgWorld) earnReorg(t *testing.T) {
	t.Helper()
	w.claim(t, claims.FieldChampion, w.person(w.priya), w.priya, day(0), claims.FirstPartyAI)
	trigger := w.claim(t, "org_change", `{"kind":"role_change","summary":"Priya now runs the Americas sites"}`, w.priya, day(0), claims.FirstPartyAI)
	w.recompute(t, day(0).Add(time.Hour), trigger)
	// The evidence is found, but a transition is never confirmed in the evaluation that first finds it.
	if rows := w.transitions(t); w.detector && (len(rows) != 1 || rows[0].Status != transitions.StatusCandidate) {
		t.Fatalf("first evaluation: %+v, want one CANDIDATE", rows)
	}
	next := w.claim(t, claims.FieldSummary, `"quarterly sync"`, "", day(0).Add(2*time.Hour), claims.FirstPartyAI)
	w.recompute(t, day(1), next)
}

func TestReorgIsEarnedByAStatedOrgChangeAboutTheChampion(t *testing.T) {
	w := newReorgWorld(t, true)
	w.earnReorg(t)

	rows := w.transitions(t)
	if len(rows) != 1 || rows[0].Status != transitions.StatusConfirmed || rows[0].FromState != "unknown" || *rows[0].ToStateCandidate != "REORG" {
		t.Fatalf("transitions = %+v, want one CONFIRMED unknown -> REORG", rows)
	}
	if rows[0].ConfirmedAt == nil || !rows[0].ConfirmedAt.Equal(day(0).Add(2*time.Hour)) || rows[0].Confidence != 1 {
		t.Errorf("confirmed_at/confidence = %v / %v, want the version's as_of and 1", rows[0].ConfirmedAt, rows[0].Confidence)
	}
	for _, f := range rows[0].SupportingFacts {
		if len(f.EvidenceRefs) == 0 {
			t.Errorf("supporting fact %s cites no evidence", f.Key)
		}
	}
	e := w.exposed(t)
	if e.RelationshipState.Value != "REORG" || e.RelationshipState.TransitionID == nil || *e.RelationshipState.TransitionID != rows[0].ID || e.OpenTransition != nil {
		t.Errorf("exposed = %+v, want REORG with no open transition", e)
	}
	w.checkTransitionContract(t)
}

func TestExpansionIsEarnedStepByStepAndNeverPrematurely(t *testing.T) {
	w := newReorgWorld(t, true)
	w.earnReorg(t)

	// New owner after the reorg, a stated expansion need and a new stakeholder: a CANDIDATE, not a state.
	w.claim(t, claims.FieldChampion, w.person(w.marco), w.marco, day(8), claims.FirstPartyAI)
	w.claim(t, claims.FieldChampionStatus, `"active"`, "", day(8), claims.FirstPartyAI)
	w.claim(t, "expansion_need", `"Roll out to the EU plants"`, "", day(9), claims.FirstPartyAI)
	trigger := w.claim(t, claims.FieldBuyingGroupMember, `{"role":"user"}`, w.owen, day(9), claims.FirstPartyAI)
	w.recompute(t, day(10), trigger)

	rows := w.transitions(t)
	if len(rows) != 2 || rows[0].Status != transitions.StatusCandidate || *rows[0].ToStateCandidate != "EXPANSION" || rows[0].FromState != "REORG" {
		t.Fatalf("transitions = %+v, want the REORG plus an open CANDIDATE EXPANSION", rows)
	}
	candidate := rows[0]
	e := w.exposed(t)
	if e.RelationshipState.Value != "REORG" || e.OpenTransition == nil || e.OpenTransition.TransitionID != candidate.ID || e.OpenTransition.Status != "CANDIDATE" {
		t.Fatalf("exposed = %+v, want REORG with the open candidate", e)
	}
	required := map[string]bool{}
	for _, m := range e.OpenTransition.MissingFacts {
		required[m.Key] = m.Required
	}
	for _, k := range []string{"owner_stabilized", "value_signal_present", "commercial_next_step"} {
		if !required[k] {
			t.Errorf("open transition should list %s as a missing required fact: %v", k, e.OpenTransition.MissingFacts)
		}
	}

	// A re-evaluation that finds nothing new records nothing (no history noise) and keeps the exposure.
	quiet := w.claim(t, claims.FieldSummary, `"quarterly sync"`, "", day(11), claims.FirstPartyAI)
	w.recompute(t, day(12), quiet)
	hist, err := transitionstore.History(context.Background(), env.DB, candidate.ID)
	if err != nil || len(hist) != 1 {
		t.Fatalf("history after a quiet recompute = %d (%v), want 1", len(hist), err)
	}
	if e := w.exposed(t); e.OpenTransition == nil || e.OpenTransition.TransitionID != candidate.ID {
		t.Errorf("a quiet recompute must keep exposing the open transition: %+v", e)
	}

	// Value, a known economic buyer and decision process, and an owner who has held the role 14 days: CONFIRMED.
	w.claim(t, "business_value", `"Cut downtime 18% at the pilot plant"`, "", day(30), claims.FirstPartyAI)
	w.claim(t, claims.FieldEconomicBuyer, w.person(w.owen), w.owen, day(30), claims.FirstPartyAI)
	trigger = w.claim(t, claims.FieldDecisionProcess, `"budget review on Oct 12"`, "", day(40), claims.FirstPartyAI)
	w.recompute(t, day(41), trigger)

	rows = w.transitions(t)
	if len(rows) != 2 || rows[0].ID != candidate.ID || rows[0].Status != transitions.StatusConfirmed || rows[0].ConfirmedAt == nil {
		t.Fatalf("transitions = %+v, want the same transition CONFIRMED", rows)
	}
	e = w.exposed(t)
	if e.RelationshipState.Value != "EXPANSION" || e.OpenTransition != nil {
		t.Errorf("exposed = %+v, want EXPANSION with nothing open", e)
	}
	hist, _ = transitionstore.History(context.Background(), env.DB, candidate.ID)
	if len(hist) != 2 || hist[0].Status != transitions.StatusCandidate || hist[1].Status != transitions.StatusConfirmed {
		t.Errorf("history = %+v, want CANDIDATE then CONFIRMED snapshots", hist)
	}
	w.checkTransitionContract(t)
}

func TestACandidateIsRejectedByDecisiveContradictionAndTheStateStays(t *testing.T) {
	w := newReorgWorld(t, true)
	w.earnReorg(t)
	w.claim(t, claims.FieldChampion, w.person(w.marco), w.marco, day(8), claims.FirstPartyAI)
	w.claim(t, "expansion_need", `"Roll out to the EU plants"`, "", day(9), claims.FirstPartyAI)
	trigger := w.claim(t, claims.FieldBuyingGroupMember, `{"role":"user"}`, w.owen, day(9), claims.FirstPartyAI)
	w.recompute(t, day(10), trigger)
	if rows := w.transitions(t); rows[0].Status != transitions.StatusCandidate {
		t.Fatalf("setup: %+v", rows[0])
	}

	trigger = w.claim(t, claims.FieldRelationshipRisk, `"high"`, "", day(11), claims.FirstPartyAI)
	w.recompute(t, day(12), trigger)

	rows := w.transitions(t)
	if rows[0].Status != transitions.StatusRejected || rows[0].RejectedAt == nil {
		t.Fatalf("transitions[0] = %+v, want REJECTED", rows[0])
	}
	decisive := false
	for _, c := range rows[0].ContradictingFacts {
		decisive = decisive || (c.Rejects != nil && *c.Rejects)
	}
	if !decisive {
		t.Errorf("a REJECTED transition must keep its decisive contradiction: %+v", rows[0].ContradictingFacts)
	}
	if e := w.exposed(t); e.RelationshipState.Value != "REORG" || e.OpenTransition != nil {
		t.Errorf("exposed = %+v, want the state to stay REORG with nothing open", e)
	}
	w.checkTransitionContract(t)
}

func TestRoutineClaimsAndThirdPartyEvidenceNeverEarnAState(t *testing.T) {
	w := newReorgWorld(t, true)
	w.claim(t, claims.FieldChampion, w.person(w.priya), w.priya, day(0), claims.FirstPartyAI)
	w.claim(t, claims.FieldChampionStatus, `"delegated"`, "", day(0), claims.FirstPartyAI)
	w.claim(t, claims.FieldStakeholderRole, `"technical_evaluator"`, w.marco, day(0), claims.FirstPartyAI)
	w.claim(t, claims.FieldOwner, w.person(w.marco), w.marco, day(1), claims.FirstPartyAI)
	trigger := w.claim(t, "org_change", `{"kind":"reorganisation","summary":"a LinkedIn alert says Priya moved"}`, w.priya, day(1), claims.ThirdParty)
	w.recompute(t, day(2), trigger)

	if rows := w.transitions(t); len(rows) != 0 {
		t.Fatalf("transitions = %+v, want none: routine claims and third-party evidence do not earn a state", rows)
	}
	if e := w.exposed(t); e.RelationshipState.Value != "unknown" || e.RelationshipState.TransitionID != nil || e.OpenTransition != nil {
		t.Errorf("exposed = %+v, want unknown", e)
	}
}

func TestWithoutADetectorTheStateStaysUnknownAndNothingIsRecorded(t *testing.T) {
	w := newReorgWorld(t, false)
	w.earnReorg(t)
	if rows := w.transitions(t); len(rows) != 0 {
		t.Fatalf("transitions = %+v, want none with the detector off", rows)
	}
	var e exposure
	raw := scalar(t, `SELECT state::text FROM account_state WHERE account_id = $1::uuid`, w.account)
	validate(t, raw)
	if err := json.Unmarshal([]byte(raw), &e); err != nil || e.RelationshipState.Value != "" {
		t.Errorf("a state written without a detector carries no relationship_state: %+v %v", e, err)
	}
}

type failingDetector struct{}

func (failingDetector) Detect(context.Context, *sql.Tx, reducer.AccountState, []reducer.OpportunityState, []string) (reducer.RelationshipState, *reducer.OpenTransition, error) {
	return reducer.RelationshipState{}, nil, errors.New("detector down")
}

func TestADetectorErrorRollsTheWholeRecomputeBack(t *testing.T) {
	resetAll(t)
	account := newAccount(t, "Acme Corp")
	act := newActivity(t, account, 1, day(0))
	clk := clock.NewFixed(day(1))
	svc := newService(t, clk, coalesce.Options{WorkerID: "w1", Detector: failingDetector{}, MaxAttempts: 1})
	insertJob(t, account, day(1), []string{act}, "", time.Time{})
	res, err := svc.Drain(context.Background())
	if err == nil || !strings.Contains(err.Error(), "detector down") || res.Failed != 1 || len(res.Recomputes) != 0 {
		t.Fatalf("drain = %+v, %v; want one failed job naming the detector", res, err)
	}
	if n := scalar(t, `SELECT count(*)::text FROM account_state WHERE account_id = $1::uuid`, account); n != "0" {
		t.Errorf("account_state rows = %s, want the recompute rolled back", n)
	}
	if n := scalar(t, `SELECT count(*)::text FROM state_history WHERE account_id = $1::uuid`, account); n != "0" {
		t.Errorf("state_history rows = %s, want the recompute rolled back", n)
	}
}

func TestSuggestionGradeAndExpiredClaimsNeverEarnAState(t *testing.T) {
	w := newReorgWorld(t, true)
	w.claim(t, claims.FieldChampion, w.person(w.priya), w.priya, day(0), claims.FirstPartyAI)
	trigger := w.claim(t, "org_change", `{"kind":"role_change","summary":"Priya now runs the Americas sites"}`, w.priya, day(0), claims.FirstPartyAI)
	// the claim is an AI suggestion below the adjudication confidence floor
	if _, err := env.DB.Exec(`UPDATE claims SET confidence = 0.3 WHERE field_path = 'org_change'`); err != nil {
		t.Fatal(err)
	}
	w.recompute(t, day(1), trigger)
	if rows := w.transitions(t); len(rows) != 0 {
		t.Fatalf("transitions = %+v, want none from a 0.3-confidence AI claim", rows)
	}
	// the same claim, expired before the evaluated as_of
	if _, err := env.DB.Exec(`UPDATE claims SET confidence = 0.9, expires_at = $1 WHERE field_path = 'org_change'`, day(0).Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	next := w.claim(t, claims.FieldSummary, `"sync"`, "", day(2), claims.FirstPartyAI)
	w.recompute(t, day(3), next)
	if rows := w.transitions(t); len(rows) != 0 {
		t.Fatalf("transitions = %+v, want none from an expired claim", rows)
	}
}

// A replay records every signal "now" (created_at is the wall clock) while the activities carry event time.
// The rules must read the signal's occurred_at, or no signal would ever look like the past.
func TestSignalsAreReadOnTheWorldClockNotTheRecordingClock(t *testing.T) {
	w := newReorgWorld(t, true)
	var evidence string
	insertSignal := func(kind string, occurred time.Time) {
		if _, err := env.DB.Exec(`INSERT INTO signals (account_id, signal_type, rule, occurred_at, evidence_refs, expires_at)
			VALUES ($1::uuid, $2, 'test', $3, jsonb_build_array(jsonb_build_object('activity_id', $4::text)),
			        CASE WHEN $2 IN ('customer_went_silent') THEN NULL ELSE $3::timestamptz + interval '14 days' END)`,
			w.account, kind, occurred, evidence); err != nil {
			t.Fatal(err)
		}
	}
	trigger := w.claim(t, "org_change", `{"kind":"restructure","summary":"the support org is merging"}`, "", day(0), claims.FirstPartyAI)
	evidence = trigger
	insertSignal("customer_went_silent", day(0))
	w.recompute(t, day(1), trigger)
	if rows := w.transitions(t); len(rows) != 1 || rows[0].Status != transitions.StatusCandidate || *rows[0].ToStateCandidate != "REORG" {
		t.Fatalf("setup: %+v, want an open CANDIDATE REORG", rows)
	}

	// created_at defaults to the wall clock, months after the activity clock of this test.
	insertSignal("champion_reactivated", day(2))
	next := w.claim(t, claims.FieldSummary, `"sync"`, "", day(3), claims.FirstPartyAI)
	w.recompute(t, day(4), next)
	rows := w.transitions(t)
	if rows[0].Status != transitions.StatusRejected {
		t.Fatalf("transitions[0] = %+v, want REJECTED by the champion's re-engagement (occurred_at day 2, as_of day 3)", rows[0])
	}
}
