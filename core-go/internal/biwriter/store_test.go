package biwriter_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/biwriter"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
	"github.com/harneet2512/gtm-work/core-go/internal/transitionstore"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var bg = context.Background()

// released is a world after the held-out event went through the real pipeline: ingest, recompute (state,
// diff, signals, trigger) and the (stand-in) graph projection.
type released struct {
	world replaytest.World
	stack *replaytest.Stack
	src   biwriter.Source
}

func releaseEvent(t *testing.T, ev replaytest.HeldOut, project bool) released {
	t.Helper()
	w := replaytest.SeedWorld(t, env.DB)
	stack := replaytest.NewStack(t, env.DB)
	stack.History(t, 2)
	res, err := stack.Ingest.Ingest(bg, ev.Event)
	if err != nil {
		t.Fatal(err)
	}
	if err := stack.Drain(bg); err != nil {
		t.Fatal(err)
	}
	if project {
		replaytest.FakeProjector{DB: env.DB}.Complete(t)
	}
	return released{world: w, stack: stack, src: biwriter.Source{ActivityID: res.ActivityID, SourceEventID: res.SourceEventID,
		OpportunityID: w.Opportunity, HeldOutEventID: ev.EventID}}
}

func newID(t *testing.T) string { return replaytest.One(t, env.DB, `SELECT gen_random_uuid()::text`) }

func writeResult(t *testing.T, f biwriter.Facts) (biwriter.Result, biwriter.Stored) {
	t.Helper()
	r, err := biwriter.Build(f, biwriter.IDs{Change: newID(t), BI: newID(t)}, replaytest.T0.Add(time.Hour))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tx, err := env.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	st, err := biwriter.Write(bg, tx, f, r)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return r, st
}

func count(t *testing.T, table string) string {
	return replaytest.One(t, env.DB, `SELECT count(*)::text FROM `+table)
}

func TestFactsAreLoadedFromTheRealPipelineAndWrittenOnce(t *testing.T) {
	r := releaseEvent(t, replaytest.NewHeldOut(2), true)
	f, err := biwriter.LoadFacts(bg, env.DB, r.src)
	if err != nil {
		t.Fatalf("LoadFacts: %v", err)
	}
	if !f.Diff.IsMaterial || f.AccountName != "Acme Corp" || f.Activity.Type != "EmailReceived" || len(f.Graph.JobIDs) == 0 {
		t.Fatalf("facts = %+v", f)
	}
	var fields []string
	for _, e := range f.Diff.Entries {
		if e.Material {
			fields = append(fields, e.Field)
		}
	}
	if !contains(fields, "blockers") {
		t.Fatalf("the SOC2 email must change the blockers, material fields = %v", fields)
	}
	if !signalTypes(f.Signals)["security_blocker_appeared"] || f.Trigger == nil || !f.Trigger.Eligible {
		t.Fatalf("signals = %+v trigger = %+v", f.Signals, f.Trigger)
	}

	res, st := writeResult(t, f)
	if !st.Created || st.ChangeID != res.Change.ID || st.BIID != res.BI.ID {
		t.Fatalf("stored = %+v", st)
	}
	if count(t, "account_changes") != "1" || count(t, "business_intelligence_updates") != "1" {
		t.Fatal("one change and one update expected")
	}

	change, update, err := biwriter.Read(bg, env.DB, st.ChangeID)
	if err != nil || update == nil {
		t.Fatalf("Read: %v %s", err, update)
	}
	v, _ := schemacheck.New()
	for name, doc := range map[string][]byte{"account_change": change, "business_intelligence_update": update} {
		if err := v.Validate(name, doc); err != nil {
			t.Fatalf("persisted %s violates its schema: %v\n%s", name, err, doc)
		}
	}
	var bi struct {
		Claims []struct {
			Statement      string
			EvidenceRefs   []json.RawMessage `json:"evidence_refs"`
			StateDiffField string            `json:"state_diff_field"`
		}
		Transition json.RawMessage
	}
	if err := json.Unmarshal(update, &bi); err != nil {
		t.Fatal(err)
	}
	if string(bi.Transition) != "null" {
		t.Errorf("no transition moved, status must be none (null), got %s", bi.Transition)
	}
	for _, c := range bi.Claims {
		if len(c.EvidenceRefs) == 0 || c.StateDiffField == "" {
			t.Errorf("persisted claim %q is uncited", c.Statement)
		}
	}
}

func TestFactsWaitForTheRecomputeAndForTheGraphProjection(t *testing.T) {
	r := releaseEvent(t, replaytest.NewHeldOut(2), false)
	if _, err := biwriter.LoadFacts(bg, env.DB, r.src); !errors.Is(err, biwriter.ErrGraphPending) {
		t.Fatalf("before the projection: %v, want ErrGraphPending", err)
	}
	replaytest.FakeProjector{DB: env.DB}.Complete(t)
	if _, err := biwriter.LoadFacts(bg, env.DB, r.src); err != nil {
		t.Fatalf("after the projection: %v", err)
	}

	w := replaytest.SeedWorld(t, env.DB)
	stack := replaytest.NewStack(t, env.DB)
	res, err := stack.Ingest.Ingest(bg, replaytest.NewHeldOut(2).Event)
	if err != nil {
		t.Fatal(err)
	}
	src := biwriter.Source{ActivityID: res.ActivityID, SourceEventID: res.SourceEventID, OpportunityID: w.Opportunity, HeldOutEventID: replaytest.NewHeldOut(2).EventID}
	if _, err := biwriter.LoadFacts(bg, env.DB, src); !errors.Is(err, biwriter.ErrDiffPending) {
		t.Fatalf("before the recompute: %v, want ErrDiffPending", err)
	}
}

func TestTheHeldOutEventGetsOneChangeAndOneUpdate(t *testing.T) {
	r := releaseEvent(t, replaytest.NewHeldOut(2), true)
	f, err := biwriter.LoadFacts(bg, env.DB, r.src)
	if err != nil {
		t.Fatal(err)
	}
	_, first := writeResult(t, f)
	_, second := writeResult(t, f)
	if second.Created || second.ChangeID != first.ChangeID || second.BIID != first.BIID {
		t.Fatalf("second write = %+v, first = %+v: the second must find the first and write nothing", second, first)
	}
	if count(t, "account_changes") != "1" || count(t, "business_intelligence_updates") != "1" {
		t.Fatal("a second write added rows")
	}
}

func TestWriteRefusesAnUncitedClaimAndTouchesNothing(t *testing.T) {
	r := releaseEvent(t, replaytest.NewHeldOut(2), true)
	f, err := biwriter.LoadFacts(bg, env.DB, r.src)
	if err != nil {
		t.Fatal(err)
	}
	res, err := biwriter.Build(f, biwriter.IDs{Change: newID(t), BI: newID(t)}, replaytest.T0)
	if err != nil {
		t.Fatal(err)
	}
	res.BI.Claims[0].EvidenceRefs = nil
	tx, _ := env.DB.Begin()
	defer func() { _ = tx.Rollback() }()
	if _, err := biwriter.Write(bg, tx, f, res); err == nil {
		t.Fatal("Write accepted an uncited claim")
	}
	_ = tx.Rollback()
	if count(t, "account_changes") != "0" || count(t, "business_intelligence_updates") != "0" {
		t.Fatal("an uncited update reached the database")
	}
}

func TestANonMaterialEventStoresTheChangeAndNoUpdate(t *testing.T) {
	bland := replaytest.HeldOut{EventID: "0e7e0000-0000-4000-8000-000000000003", Event: replaytest.Email(9, replaytest.T0.Add(time.Hour), "inbound", "Thanks for the update")}
	r := releaseEvent(t, bland, true)
	f, err := biwriter.LoadFacts(bg, env.DB, r.src)
	if err != nil {
		t.Fatal(err)
	}
	if f.Diff.IsMaterial {
		t.Skipf("the pipeline found a material change in the bland email: %+v", f.Diff.Entries)
	}
	_, st := writeResult(t, f)
	if !st.Created || st.BIID != "" {
		t.Fatalf("stored = %+v, want a change and no update", st)
	}
	if count(t, "account_changes") != "1" || count(t, "business_intelligence_updates") != "0" {
		t.Fatal("a non-material event must not produce an update")
	}
	change, update, err := biwriter.Read(bg, env.DB, st.ChangeID)
	if err != nil || update != nil {
		t.Fatalf("Read: %v, update %s", err, update)
	}
	v, _ := schemacheck.New()
	if err := v.Validate("account_change", change); err != nil {
		t.Fatalf("account_change: %v\n%s", err, change)
	}
}

// A transition the event touched is restated with its missing facts.
func TestTheTransitionTheEventMovedIsRestatedWithItsMissingFacts(t *testing.T) {
	r := releaseEvent(t, replaytest.NewHeldOut(2), true)
	to := "EXPANSION"
	rec := transitions.Record{AccountID: r.world.Account, FromState: "REORG", ToStateCandidate: &to, Status: transitions.StatusCandidate,
		TriggerActivityIDs: []string{r.src.ActivityID}, ContradictingFacts: []transitions.FactResult{}, Confidence: 0.4,
		SupportingFacts: []transitions.FactResult{{Key: "expansion_need_stated", Description: "A broader rollout was asked for.", Required: true, Satisfied: true,
			EvidenceRefs: []transitions.Ref{{ActivityID: r.src.ActivityID, Quote: "the SOC2 report"}}, SignalIDs: []string{}}},
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
	if f.Transition == nil || f.Transition.ID != stored.ID || len(f.Transition.Missing) != 1 {
		t.Fatalf("transition fact = %+v", f.Transition)
	}
	_, st := writeResult(t, f)
	_, update, err := biwriter.Read(bg, env.DB, st.ChangeID)
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := schemacheck.New()
	if err := v2.Validate("business_intelligence_update", update); err != nil {
		t.Fatalf("%v\n%s", err, update)
	}
	for _, want := range []string{`"status": "CANDIDATE"`, `"owner_stabilized"`, `"touched_by_event": true`, "needed: owner stabilized"} {
		if !strings.Contains(spaced(update), want) {
			t.Errorf("update lacks %s:\n%s", want, update)
		}
	}
}

// The writer's update is what core serves and what Slack Message 1 renders.
func TestSlackMessageOneRendersTheWritersUpdateFromTheCoreEndpoint(t *testing.T) {
	r := releaseEvent(t, replaytest.NewHeldOut(2), true)
	f, err := biwriter.LoadFacts(bg, env.DB, r.src)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := writeResult(t, f)

	svc, err := strategystore.New(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := svc.LatestBusinessIntelligence(bg, r.world.Account)
	if err != nil {
		t.Fatalf("the existing read endpoint does not serve the writer's update: %v", err)
	}
	v, _ := schemacheck.New()
	if err := v.Validate("business_intelligence_update", doc); err != nil {
		t.Fatalf("served update violates its schema: %v\n%s", err, doc)
	}
	var served slacksurface.BusinessIntelligenceUpdate
	if err := json.Unmarshal(doc, &served); err != nil {
		t.Fatal(err)
	}
	if served.ID != res.BI.ID || served.AccountChangeID != res.Change.ID {
		t.Fatalf("served %s/%s, wrote %s/%s", served.ID, served.AccountChangeID, res.BI.ID, res.Change.ID)
	}

	msg := slacksurface.RenderBI(served, "Acme Corp", "https://app.example/map", "")
	blocks, _ := json.Marshal(msg.Blocks)
	rendered := string(blocks) + msg.Text
	for _, c := range res.BI.Claims {
		if !strings.Contains(rendered, firstWords(c.Statement)) {
			t.Errorf("M1 does not show the claim %q", c.Statement)
		}
	}
	for _, want := range []string{"What changed", "Evidence:", "Why this matters", "Acme Corp changed", "View account map"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("M1 lacks %q:\n%s", want, rendered)
		}
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

func signalTypes(in []biwriter.Signal) map[string]bool {
	out := map[string]bool{}
	for _, s := range in {
		out[s.Type] = true
	}
	return out
}

// spaced re-indents compact JSON so substring checks can use `"key": "value"`.
func spaced(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, _ := json.MarshalIndent(v, "", " ")
	return string(out)
}

func firstWords(s string) string {
	words := strings.Fields(s)
	if len(words) > 3 {
		words = words[:3]
	}
	return strings.Join(words, " ")
}
