package recompute

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var bg = context.Background()

// world is a seeded episode (the HAR-129 demo shape: three candidates, each with its bundle) with the real
// strategy service on top, so the edits, the send-time re-evaluation and the HumanDelta are the real ones.
type world struct {
	t      *testing.T
	seed   strategytest.Seeded
	strat  *strategystore.Service
	recomp *Service
	cand   int // the index of the candidate the test chooses (0-based)
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := ctxfixture.Get(t, env.DB)
	strat, err := strategystore.New(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	recomp, err := New(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &world{t: t, seed: strategytest.Seed(t, env.DB, w.AccountA), strat: strat, recomp: recomp, cand: 1}
}

// use chooses another candidate for the test (0 is Ghost's first preference).
func (w *world) use(i int) *world { w.cand = i; return w }

// addOldEval appends a judged result to the chosen candidate's bundle, as the orchestrator does for each eval: an
// eval on the OLD artifact that the send-time re-evaluation re-runs (the seeded bundles hold semantic evals only).
func (w *world) addOldEval(evalType, kind, verdict string) string {
	w.t.Helper()
	id := strategytest.NewID()
	if _, err := env.DB.Exec(`UPDATE eval_bundles SET items = items || jsonb_build_array(jsonb_build_object(
 'eval_type', $2::text, 'relevance_reason', 'seeded', 'verdict', $3::text, 'metadata', '{}'::jsonb,
 'result', jsonb_build_object('id', $4::text, 'agent_run_id', agent_run_id::text, 'draft_index', draft_index, 'eval_type', $2::text,
   'eval_version', $2::text || ':v1', 'kind', $5::text, 'verdict', $3::text, 'blocking', false)))
 WHERE id = $1::uuid`, w.seed.Bundles[w.cand], evalType, verdict, id, kind); err != nil {
		w.t.Fatalf("add a bundle result: %v", err)
	}
	return id
}

func (w *world) choose(edit func(*strategystore.DecisionRequest)) {
	w.t.Helper()
	req := strategystore.DecisionRequest{SelectedCandidateID: w.seed.Candidates[w.cand], Surface: "api", ActorLabel: "rep"}
	if edit != nil {
		edit(&req)
	}
	if _, _, err := w.strat.RecordDecision(bg, w.seed.RunID, req); err != nil {
		w.t.Fatalf("choose: %v", err)
	}
}

func (w *world) send(decision string) {
	w.t.Helper()
	if _, err := w.strat.Send(bg, w.seed.RunID, strategystore.SendRequest{Decision: decision, Surface: "api", ActorLabel: "rep"}); err != nil {
		w.t.Fatalf("send: %v", err)
	}
}

func (w *world) doc() Invalidation {
	w.t.Helper()
	d, err := w.recomp.Recomputation(bg, w.seed.RunID)
	if err != nil {
		w.t.Fatal(err)
	}
	valid(w.t, d)
	return d
}

func (w *world) removeCC(r *strategystore.DecisionRequest) { r.FinalCC = &[]strategystore.Recipient{} }

// changeSubject edits only the subject: the artifact is the candidate's own, byte for byte, but for that.
func (w *world) changeSubject(r *strategystore.DecisionRequest) {
	var raw []byte
	if err := env.DB.QueryRow(`SELECT full_action_artifact::text FROM strategy_candidates WHERE id = $1::uuid`, w.seed.Candidates[w.cand]).Scan(&raw); err != nil {
		w.t.Fatal(err)
	}
	var art strategystore.Artifact
	if err := json.Unmarshal(raw, &art); err != nil {
		w.t.Fatal(err)
	}
	subject := "A different subject"
	art.Subject = &subject
	r.FinalArtifact = &art
}

func TestARealRecipientEditInvalidatesRecipientDependentEvalsAndPreservesAccountState(t *testing.T) {
	w := newWorld(t)
	oldRecipient := w.addOldEval("recipient_correctness", "deterministic", "pass")
	w.choose(w.removeCC)
	w.send("send")

	d := w.doc()
	if d.Status != Reevaluated || !d.Edited || d.HumanDeltaID == nil || len(d.Entries) != 1 {
		t.Fatalf("status %s edited %v delta %v entries %d", d.Status, d.Edited, d.HumanDeltaID, len(d.Entries))
	}
	e := d.Entries[0]
	if e.Edit.Field != Recipients || e.Edit.Kind != "recipient_removed" {
		t.Fatalf("edit = %+v", e.Edit)
	}
	if got := labels(e.Invalidated, EvalResult); !slices.Equal(got, []string{"champion_continuity", "recipient_correctness"}) {
		t.Errorf("invalidated evals = %v", got)
	}
	// the recomputed recipient_correctness is a real send-time row that replaces the old one
	var replacement string
	for _, r := range e.Recomputed {
		if r.Kind == EvalResult && r.Label == "recipient_correctness" && r.Replaces != nil && *r.Replaces == oldRecipient {
			replacement = *r.RefID
		}
	}
	if replacement == "" {
		t.Fatalf("recomputed = %+v: the send-time recipient_correctness must replace the old result", e.Recomputed)
	}
	if got := scalar(t, `SELECT count(*)::text FROM send_eval_results WHERE eval_run_id = $1::uuid`, replacement); got != "1" {
		t.Errorf("the replacement %s is not a linked send-time row", replacement)
	}
	if len(refsOf(e.Recomputed, FinalArtifact)) != 1 || *e.Recomputed[0].RefID == "" {
		t.Errorf("the re-evaluated final artifact is recomputed: %+v", e.Recomputed)
	}
	// semantic evals are not re-run at send time: said, not hidden
	if !slices.Contains(labels(e.NotRecomputed, EvalResult), "champion_continuity") || len(refsOf(e.NotRecomputed, RankingRat)) != 1 {
		t.Errorf("not_recomputed = %+v", e.NotRecomputed)
	}
	// the account state: the same version before and after, preserved; cta_calibration does not read the recipients
	if d.AccountState.Preserved == nil || !*d.AccountState.Preserved || d.AccountState.VersionBefore == nil || d.AccountState.VersionAfter == nil || *d.AccountState.VersionBefore != *d.AccountState.VersionAfter {
		t.Errorf("account state = %+v", d.AccountState)
	}
	if !slices.Contains(labels(e.Preserved, EvalResult), "cta_calibration") || len(refsOf(e.Preserved, AccountState)) != 1 {
		t.Errorf("preserved = %+v", e.Preserved)
	}
	if want := scalar(t, `SELECT id::text FROM human_deltas WHERE decision_episode_id = $1::uuid`, w.seed.EpisodeID); *d.HumanDeltaID != want {
		t.Errorf("human delta = %s, want %s", *d.HumanDeltaID, want)
	}
}

func TestARealSubjectEditLeavesTheRecipientEvalsPreserved(t *testing.T) {
	w := newWorld(t).use(0)
	w.addOldEval("recipient_correctness", "deterministic", "pass")
	w.choose(w.changeSubject)
	w.send("send")

	d := w.doc()
	if d.Status != Reevaluated || len(d.Entries) != 1 || d.Entries[0].Edit.Field != Subject {
		t.Fatalf("%+v", d.Entries)
	}
	e := d.Entries[0]
	if got := labels(e.Invalidated, EvalResult); !slices.Equal(got, []string{"cta_calibration"}) {
		t.Errorf("invalidated evals = %v, want only cta_calibration", got)
	}
	if got := labels(e.Preserved, EvalResult); !slices.Equal(got, []string{"champion_continuity", "recipient_correctness"}) {
		t.Errorf("preserved evals = %v: neither reads the subject, and the re-run of recipient_correctness agreed", got)
	}
}

func TestARealUneditedSendHasAnEmptyInvalidationSet(t *testing.T) {
	w := newWorld(t).use(0)
	w.addOldEval("recipient_correctness", "deterministic", "pass")
	w.choose(nil)
	w.send("send")
	d := w.doc()
	if d.Status != Unedited || d.Edited || len(d.Entries) != 0 || d.HumanDeltaID != nil {
		t.Fatalf("%+v", d)
	}
	if len(refsOf(d.PreservedOverall, AccountState)) != 1 || len(refsOf(d.PreservedOverall, EvalResult)) == 0 {
		t.Errorf("everything is preserved: %+v", d.PreservedOverall)
	}
}

func TestEditsSavedButNotSentAreInvalidatedAndNothingIsRecomputed(t *testing.T) {
	w := newWorld(t)
	w.choose(w.removeCC)
	d := w.doc()
	if d.Status != EditsPend || !d.Edited || d.HumanDeltaID != nil || len(d.Entries) != 1 {
		t.Fatalf("%+v", d)
	}
	e := d.Entries[0]
	if len(e.Invalidated) == 0 || len(e.Recomputed) != 0 || len(e.NotRecomputed) != len(e.Invalidated) || d.AccountState.VersionAfter != nil {
		t.Errorf("before the send nothing is recomputed: %+v", e)
	}
}

func TestADecisionNobodyMadeIsNotDecidedAndADiscardInvalidatesNothing(t *testing.T) {
	w := newWorld(t)
	if d := w.doc(); d.Status != NotDecided || len(d.Entries) != 0 {
		t.Fatalf("nothing chosen: %+v", d)
	}
	w.choose(w.removeCC)
	w.send("discard")
	if d := w.doc(); d.Status != Discarded || len(d.Entries) != 0 || !d.Edited {
		t.Fatalf("a discard: %+v", d)
	}
}

func TestASendWhoseBatchIsNotLinkedSaysWhatItCannotShow(t *testing.T) {
	w := newWorld(t)
	w.choose(w.removeCC)
	w.send("send")
	if _, err := env.DB.Exec(`DELETE FROM send_eval_results`); err != nil { // a send recorded before the link existed
		t.Fatal(err)
	}
	d := w.doc()
	if d.Status != Unavailable || len(d.Entries) != 1 || len(d.Entries[0].Recomputed) != 0 || len(d.Entries[0].NotRecomputed) == 0 {
		t.Fatalf("%+v", d)
	}
}

func TestARefusedSendBatchNeverShowsUpAsRecomputed(t *testing.T) {
	w := newWorld(t)
	noEmail := strategytest.NewID()
	if _, err := env.DB.Exec(`INSERT INTO people (id, kind, display_name, account_id) VALUES ($1::uuid, 'contact', 'No Email (recompute)', $2::uuid)`, noEmail, w.seed.AccountID); err != nil {
		t.Fatal(err)
	}
	w.choose(func(r *strategystore.DecisionRequest) {
		r.FinalTo = &[]strategystore.Recipient{{PersonID: noEmail, Role: "to"}}
	})
	if _, err := w.strat.Send(bg, w.seed.RunID, strategystore.SendRequest{Decision: "send", Surface: "api", ActorLabel: "rep"}); err == nil {
		t.Fatal("the send to a contact with no email must be refused")
	}
	refused := scalar(t, `SELECT COALESCE(string_agg(id::text, ','), '') FROM eval_runs WHERE agent_run_id = $1::uuid AND blocking`, w.seed.RunID)
	if refused == "" {
		t.Fatal("the refused send persisted no blocking result")
	}
	if d := w.doc(); d.Status != EditsPend {
		t.Fatalf("after a refused send the decision is still pending: %s", d.Status)
	}

	w.choose(func(r *strategystore.DecisionRequest) {
		r.FinalTo = &[]strategystore.Recipient{{PersonID: w.seed.Marco, Role: "to"}}
		w.changeSubject(r)
	})
	// the blocking failure of the refused artifact is not in the sent batch
	w.send("send")
	d := w.doc()
	if d.Status != Reevaluated {
		t.Fatalf("status %s", d.Status)
	}
	for _, e := range d.Entries {
		for _, r := range e.Recomputed {
			if r.Kind == EvalResult && r.RefID != nil && containsID(refused, *r.RefID) {
				t.Errorf("recomputed %s belongs to the refused batch, not the sent one", *r.RefID)
			}
		}
	}
}

func containsID(csv, id string) bool {
	for i := 0; i+len(id) <= len(csv); i++ {
		if csv[i:i+len(id)] == id {
			return true
		}
	}
	return false
}

func TestAnUnknownRunOrARunWithoutAnEpisodeIsNotFound(t *testing.T) {
	w := newWorld(t)
	for name, id := range map[string]string{
		"unknown":           "99999999-9999-4999-8999-999999999999",
		"malformed":         "nope",
		"a run with no set": ctxfixture.FreshRun(t, env.DB, ctxfixture.Get(t, env.DB).AccountB, "pending"),
	} {
		if _, err := w.recomp.Recomputation(bg, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if _, err := New(nil, nil); err == nil {
		t.Error("a nil database was accepted")
	}
}

func scalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	var v string
	if err := env.DB.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func preserved(d Invalidation) string {
	switch p := d.AccountState.Preserved; {
	case p == nil:
		return "unknown"
	case *p:
		return "true"
	default:
		return "false"
	}
}

// "Preserved" is proven by the state the send-time evaluation stored (its version and digest), compared with the
// state the run read: change either stored value and the claim goes away.
func TestAccountStatePreservedIsProvenFromTheStoredBasisNeverAssumed(t *testing.T) {
	w := newWorld(t)
	w.choose(w.removeCC)
	w.send("send")
	if got := preserved(w.doc()); got != "true" {
		t.Fatalf("the send read the state the run read: preserved = %s, want true", got)
	}
	stored := scalar(t, `SELECT string_agg(DISTINCT state_hash, ',') FROM send_eval_results`)
	if len(stored) != 64 {
		t.Fatalf("the send stored basis %q: want exactly one sha-256", stored)
	}

	// the stored digest differs from the digest of the state the run read: same version, other content
	if _, err := env.DB.Exec(`UPDATE send_eval_results SET state_hash = repeat('0', 64)`); err != nil {
		t.Fatal(err)
	}
	d := w.doc()
	if got := preserved(d); got != "false" || len(refsOf(d.PreservedOverall, AccountState)) != 0 || len(refsOf(d.Entries[0].Preserved, AccountState)) != 0 {
		t.Errorf("a different digest: preserved = %s, overall %+v: the state must not be claimed preserved", got, d.PreservedOverall)
	}

	// the stored version differs from the run's
	if _, err := env.DB.Exec(`UPDATE send_eval_results SET state_hash = $1, state_version = state_version + 1`, stored); err != nil {
		t.Fatal(err)
	}
	if got := preserved(w.doc()); got != "false" {
		t.Errorf("a different version: preserved = %s, want false", got)
	}
}

func TestAccountStatePreservedIsUnknownWhenTheRunsStateCannotBeRead(t *testing.T) {
	w := newWorld(t)
	w.choose(w.removeCC)
	w.send("send")
	if _, err := env.DB.Exec(`UPDATE agent_runs SET state_version = NULL WHERE id = $1::uuid`, w.seed.RunID); err != nil {
		t.Fatal(err)
	}
	d := w.doc()
	if got := preserved(d); got != "unknown" || len(refsOf(d.PreservedOverall, AccountState)) != 0 {
		t.Fatalf("a run with no pinned state version: preserved = %s, want unknown (no claim)", got)
	}
}
