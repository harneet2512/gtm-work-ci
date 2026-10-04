package abcrun_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/abcrun"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

func repoFile(t testing.TB, rel string) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return filepath.Join(dir, rel)
		}
		if filepath.Dir(dir) == dir {
			t.Fatalf("%s not found", rel)
		}
		dir = filepath.Dir(dir)
	}
}

// scriptedWorker stands in for the model: it answers /v1/strategies from the guidance the request carries, so the
// plumbing (arms, stores, labels, counterfactual, guard, corrections) is tested without any model.
type scriptedWorker struct {
	t       testing.TB
	nstrats int
	holdFor map[string]bool // knowledge ids that, when applicable in the guidance, make the agent hold the price
	seen    []string        // every request, as the worker would receive it
}

func (w *scriptedWorker) person(email string) string {
	w.t.Helper()
	return scalar(w.t, `SELECT id::text FROM people WHERE primary_email = $1`, email)
}

func (w *scriptedWorker) Strategies(_ context.Context, req workerclient.StrategiesRequest) (workerclient.StrategiesResponse, error) {
	w.nstrats++
	raw, _ := json.Marshal(req)
	w.seen = append(w.seen, string(raw))
	applicable := []string{}
	if len(req.DecisionGuidance) > 0 {
		var g struct {
			Supporting []struct {
				ID      string `json:"knowledge_id"`
				Applies bool   `json:"applies"`
			} `json:"supporting_knowledge"`
		}
		_ = json.Unmarshal(req.DecisionGuidance, &g)
		for _, s := range g.Supporting {
			if s.Applies {
				applicable = append(applicable, s.ID)
			}
		}
	}
	top := struct{ strategy, title, desc, body string }{"offer_price_concession", "Offer a price concession", "Reduce the quoted amount to keep the deal moving",
		"Hi Dana, we can reduce the quoted amount if you sign this quarter."}
	var cited []string
	for _, id := range applicable {
		if w.holdFor[id] {
			top = struct{ strategy, title, desc, body string }{"reinforce_value_case", "Reinforce the value case", "Hold the quoted price and walk through the return",
				"Hi Dana, here is where the return comes from: time saved, fewer reviews, support included."}
			cited = append(cited, id)
		}
	}
	buyer := scalar(w.t, `SELECT id::text FROM people WHERE account_id = $1::uuid AND kind = 'contact' ORDER BY id LIMIT 1`, req.AccountID)
	rep, colleague := w.person("sam.rep@ghostvendor.com"), w.person("priya.colleague@ghostvendor.com")
	activity := req.TriggerContext.TriggerActivityIDs[0]
	mk := func(rank int, strategy, title, desc, class, action, channel, body string, to, cc []string, refs []string) workerclient.Candidate {
		recips := func(ids []string, role string) []workerclient.Recipient {
			out := []workerclient.Recipient{}
			for _, id := range ids {
				out = append(out, workerclient.Recipient{PersonID: id, Role: role, Why: "the " + role})
			}
			return out
		}
		subject := "Re: proposal"
		if channel != "email" {
			subject = ""
		}
		var subj *string
		if subject != "" {
			subj = &subject
		}
		return workerclient.Candidate{
			CandidateID: uuidFor(req.RunID, rank), StrategyType: strategy, Title: title, Description: desc, Ranking: rank, PreferredByAgent: rank == 1,
			Rationale: "plausible for this state", StateRefs: []string{"stage"}, EvidenceRefs: []workerclient.EvidenceRef{{ActivityID: activity}},
			KnowledgeRefs: refs, ActionType: action, ActionClass: class,
			FiveQuestions: workerclient.FiveQuestions{WhatChanged: "The buyer objected.", WhyStateChanged: "They wrote first-party.", WhatRemainsUnknown: "Their budget.",
				PriorKnowledgeApplies: "see guidance", WhyNextAction: "It addresses the objection."},
			To: recips(to, "to"), CC: recips(cc, "cc"), Subject: subj, FullActionArtifact: workerclient.Artifact{Channel: channel, Subject: subj, Body: body}, Preview: body,
		}
	}
	return workerclient.StrategiesResponse{Model: "scripted/model", ToolCalls: 2, Candidates: []workerclient.Candidate{
		mk(1, top.strategy, top.title, top.desc, "REPLY", "send_email", "email", top.body, []string{buyer}, nil, cited),
		mk(2, "schedule_walkthrough_call", "Schedule a walkthrough", "Offer a call with a colleague", "MEETING", "schedule_meeting", "email",
			"Could we set up a short call with my colleague to walk through the details?", []string{buyer}, []string{colleague}, nil),
		mk(3, "ask_internal_owner", "Ask the account owner", "Check the buyer's budget cycle", "ASK_RESEARCH", "internal_note", "slack",
			"Please check which budget cycle the customer is in.", []string{rep}, nil, nil),
	}}, nil
}

func (w *scriptedWorker) Judge(context.Context, workerclient.JudgeRequest) (workerclient.JudgeResponse, error) {
	return workerclient.JudgeResponse{}, errors.New("the generation worker must never judge")
}

func (w *scriptedWorker) Revise(context.Context, workerclient.ReviseRequest) (workerclient.ReviseResponse, error) {
	return workerclient.ReviseResponse{}, errors.New("the generation worker must never revise")
}

func uuidFor(run string, rank int) string {
	return "00000000-0000-4000-8000-" + strings.Repeat("0", 8) + string(rune('0'+rank)) + run[len(run)-3:]
}

const (
	kPrice = "5a5a0000-0000-4000-8000-000000000202"
	kOther = "5a5a0000-0000-4000-8000-000000000204"
)

func learned(id, key, point string, sig []knowledge.Condition) abcrun.LearnedItem {
	cut := time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC)
	ev := []abcrun.EvidenceLine{
		{Kind: "decision_episode", RefID: "e1" + id[2:], At: cut.Add(-90 * 24 * time.Hour)},
		{Kind: "business_outcome", RefID: "01" + id[2:], OutcomeType: "closed_won", At: cut.Add(-60 * 24 * time.Hour)},
		{Kind: "customer_reaction", RefID: "c1" + id[2:], Polarity: "positive", At: cut.Add(-59 * 24 * time.Hour)},
	}
	return abcrun.LearnedItem{ID: id, Key: key, DecisionPoint: point, Title: "Learned " + point, SituationSignature: sig, CreatedAt: cut,
		Guidance:              knowledge.Guidance{Summary: "Learned from previous deals.", Do: []string{"Hold the quoted amount."}, Dont: []string{}},
		SourceDecisionEpisode: "e1" + id[2:], Evidence: ev}
}

func items() []abcrun.LearnedItem {
	return []abcrun.LearnedItem{
		learned(kPrice, "K202", "price_pushback", []knowledge.Condition{{Field: "stage", Op: "eq", Value: "Quote"}, {Field: "objections", Op: "contains", Value: "pricing"}}),
		learned(kOther, "K204", "second_quote", []knowledge.Condition{{Field: "stage", Op: "eq", Value: "Negotiation"}}),
	}
}

type rig struct {
	runner  *abcrun.Runner
	worker  *abcrun.GenerationWorker
	scripts *scriptedWorker
	guards  int
}

// onlyIDs limits the situations a rig runs (nil: all).
var onlyIDs []string

func newRig(t *testing.T, p abcrun.Pack, guard func(rg *rig) abcrun.Guard) *rig {
	t.Helper()
	b := newBuilder(t)
	rules, err := knowledge.LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	routing, err := orchestrator.LoadRouting(repoFile(t, "contracts/transitions/routing.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := runtoken.NewSigner([]byte("0123456789abcdef0123456789abcdef"), orchestrator.RequiredTokenTTL(true)+time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	rg := &rig{scripts: &scriptedWorker{t: t, holdFor: map[string]bool{kPrice: true}}}
	rg.worker = &abcrun.GenerationWorker{Inner: rg.scripts}
	learning := abcrun.Learning{Version: abcrun.LearningVersion, Cutoff: time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC), Knowledge: items()}
	opts := abcrun.Options{DB: env.DB, Pack: p, Learning: learning, Rules: rules, Routing: routing, Worker: rg.worker, Signer: signer, Builder: b, Pulls: 4, Only: onlyIDs}
	if guard != nil {
		opts.Guard = guard(rg)
	}
	if rg.runner, err = abcrun.NewRunner(opts); err != nil {
		t.Fatal(err)
	}
	return rg
}

func TestTheThreeArmsRunThroughTheRealOrchestratorAndDifferOnlyInKnowledge(t *testing.T) {
	rg := newRig(t, pack("discriminating", "exception"), func(rg *rig) abcrun.Guard {
		return func(_ context.Context, store []knowledge.Knowledge, empty bool) error {
			rg.guards++
			if !empty {
				return errors.New("the store was not empty before learning")
			}
			return nil
		}
	})
	out, err := rg.runner.Run(bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Situations) != 2 || !out.EmptyBeforeLearning || len(out.Store) != 2 {
		t.Fatalf("output = %d situations, empty %v, store %d", len(out.Situations), out.EmptyBeforeLearning, len(out.Store))
	}
	for _, k := range out.Store {
		if k.Status != knowledge.StatusProvisional && k.Status != knowledge.StatusSupported && k.Status != knowledge.StatusConfirmed {
			t.Fatalf("%s earned status %s from its evidence, want an applicable rung", *k.Key, k.Status)
		}
	}

	disc := out.Situations[0]
	a, b, c := disc.Arms["A"], disc.Arms["B"], disc.Arms["C"]
	if a == nil || b == nil || c == nil {
		t.Fatalf("a discriminating situation with something irrelevant runs A, B and C: %v", disc.Arms)
	}
	if a.Candidate.StrategyType != "offer_price_concession" || c.Candidate.StrategyType != "offer_price_concession" {
		t.Fatalf("A and C get no applicable knowledge and must decide alike: %s / %s", a.Candidate.StrategyType, c.Candidate.StrategyType)
	}
	if b.Candidate.StrategyType != "reinforce_value_case" || len(b.Knowledge.Cited) != 1 || b.Knowledge.Cited[0] != kPrice {
		t.Fatalf("B applies the learned item: %+v", b.Candidate.StrategyType)
	}
	if len(b.Knowledge.Applicable) != 1 || b.Knowledge.Applicable[0] != kPrice || len(a.Knowledge.Retrieved) != 0 {
		t.Fatalf("knowledge trails: B %+v A %+v", b.Knowledge, a.Knowledge)
	}
	if len(c.Knowledge.Retrieved) != 1 || c.Knowledge.Retrieved[0] != kOther || len(c.Knowledge.Applicable) != 0 {
		t.Fatalf("C retrieves the irrelevant item and applies none: %+v", c.Knowledge)
	}
	if disc.Labels[kPrice] != knowledge.LabelApplies || disc.Labels[kOther] != knowledge.LabelDoesNotApply {
		t.Fatalf("labels = %v", disc.Labels)
	}

	exc := out.Situations[1]
	if exc.Arms["C"] != nil {
		t.Fatal("exception situations compare B with A only")
	}
	eb := exc.Arms["B"]
	if len(eb.Knowledge.Retrieved) != 1 || eb.Knowledge.Retrieved[0] != kPrice || len(eb.Knowledge.Applicable) != 0 || len(eb.Knowledge.Cited) != 0 {
		t.Fatalf("the learned item must be retrieved, found not to apply and not used: %+v", eb.Knowledge)
	}
	if eb.Candidate.StrategyType != exc.Arms["A"].Candidate.StrategyType {
		t.Fatal("with nothing applicable B decides like A on the exception situation")
	}
	if out.Calls.GenerationRuns != 5 || rg.scripts.nstrats != 5 { // disc: A+B (one run with the counterfactual) + C; exception: A+B
		t.Fatalf("generations = %d (%d scripted), want 5", out.Calls.GenerationRuns, rg.scripts.nstrats)
	}
	if rg.guards < 3 {
		t.Fatalf("the hard block ran %d times, want before every arm", rg.guards)
	}
	if a.Corrections == nil || b.Corrections == nil {
		t.Fatal("corrections are an empty list, never nil")
	}
}

func TestAGuardFailureAbortsTheWholeRunBeforeAnyModelCall(t *testing.T) {
	rg := newRig(t, pack("discriminating"), func(*rig) abcrun.Guard {
		return func(context.Context, []knowledge.Knowledge, bool) error { return errors.New("seeded knowledge found") }
	})
	if _, err := rg.runner.Run(bg); err == nil || !strings.Contains(err.Error(), "seeded knowledge found") {
		t.Fatalf("err = %v, want the guard's refusal", err)
	}
	if rg.scripts.nstrats != 0 {
		t.Fatalf("a model was called %d times after the guard refused", rg.scripts.nstrats)
	}
}

func TestAnExceptionSituationWhereTheItemAppliesIsASelectionError(t *testing.T) {
	rg := newRig(t, pack("exception"), nil) // the fixture world is in Quote with a pricing objection: the item applies
	rg2, err := rg.runner.Run(bg)
	if err == nil || !strings.Contains(err.Error(), "selection error") {
		t.Fatalf("out = %+v err = %v, want a selection error before any model call", rg2, err)
	}
	if rg.scripts.nstrats != 0 {
		t.Fatal("no model call may precede a selection error")
	}
}

func TestTheSameExperimentTwiceSendsTheWorkerByteIdenticalRequestsAndProducesIdenticalArms(t *testing.T) {
	run := func() (*rig, []byte) {
		rg := newRig(t, pack("discriminating", "exception"), nil)
		out, err := rg.runner.Run(bg)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(out.Situations)
		if err != nil {
			t.Fatal(err)
		}
		return rg, raw
	}
	first, firstOut := run()
	second, secondOut := run()
	if len(first.scripts.seen) != 5 || len(first.scripts.seen) != len(second.scripts.seen) {
		t.Fatalf("requests: %d and %d, want 5 each", len(first.scripts.seen), len(second.scripts.seen))
	}
	for i := range first.scripts.seen {
		if first.scripts.seen[i] != second.scripts.seen[i] {
			t.Fatalf("request %d differs between two runs of the same experiment (a recorded call could not be replayed):\n%s\n%s", i, first.scripts.seen[i], second.scripts.seen[i])
		}
	}
	if string(firstOut) != string(secondOut) {
		t.Fatal("the arms differ between two runs of the same experiment")
	}
}

func TestAControlSituationRunsIrrelevantKnowledgeAgainstTheCounterfactualAndNothingElse(t *testing.T) {
	p := pack("discriminating", "control")
	p.Situations[1].DecisionPoint = "technical_reply" // no learned item for this decision point; the world is pre-Quote
	rg := newRig(t, p, nil)
	out, err := rg.runner.Run(bg)
	if err != nil {
		t.Fatal(err)
	}
	ctl := out.Situations[1]
	if ctl.Arms["B"] != nil || ctl.Arms["C"] == nil || ctl.Arms["A"] == nil {
		t.Fatalf("a control situation runs C against A only: %v", ctl.Arms)
	}
	c := ctl.Arms["C"]
	if len(c.Knowledge.Retrieved) != 2 || len(c.Knowledge.Applicable) != 0 || len(c.Knowledge.Cited) != 0 {
		t.Fatalf("irrelevant knowledge is retrieved, applies nowhere and is not used: %+v", c.Knowledge)
	}
	if c.Candidate.StrategyType != ctl.Arms["A"].Candidate.StrategyType {
		t.Fatal("with nothing applicable C decides like A")
	}
	if out.Calls.GenerationRuns != 5 { // discriminating: B + A + C (the second item does not apply there); control: C + A
		t.Fatalf("generations = %d, want 5", out.Calls.GenerationRuns)
	}
}

func TestASituationSendsTheWorkerTheSameRequestsWhateverRanBeforeIt(t *testing.T) {
	onlyIDs = nil
	all := newRig(t, pack("discriminating", "exception"), nil)
	if _, err := all.runner.Run(bg); err != nil {
		t.Fatal(err)
	}
	onlyIDs = []string{"S-2"}
	defer func() { onlyIDs = nil }()
	alone := newRig(t, pack("discriminating", "exception"), nil)
	if _, err := alone.runner.Run(bg); err != nil {
		t.Fatal(err)
	}
	if len(alone.scripts.seen) != 2 || len(all.scripts.seen) != 5 {
		t.Fatalf("requests: %d alone, %d together", len(alone.scripts.seen), len(all.scripts.seen))
	}
	for i, got := range alone.scripts.seen {
		if got != all.scripts.seen[3+i] {
			t.Fatalf("S-2 request %d depends on what ran before it (a recorded call would not replay):\n%s\n%s", i, got, all.scripts.seen[3+i])
		}
	}
}
