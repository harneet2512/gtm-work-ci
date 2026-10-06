package orchestrator_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

var env *storetest.Env

func TestMain(m *testing.M) { os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e })) }

var bg = context.Background()

// wallNow is the fixed wall clock of the tests: far after every replayed event, so any use of it as "now" is visible.
var wallNow = time.Date(2035, 1, 1, 12, 0, 0, 0, time.UTC)

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

func config(t testing.TB) orchestrator.Config {
	t.Helper()
	rules, err := knowledge.LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	routing, err := orchestrator.LoadRouting(repoFile(t, "contracts/transitions/routing.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return orchestrator.Config{WorkspaceID: "workspace-test", Knowledge: rules, Routing: routing}
}

// tokenTTL is the test signer's token life: longer than one run needs (generation, judging, one revision).
const tokenTTL = 45 * time.Minute

func signerFor(t testing.TB, ttl time.Duration) *runtoken.Signer {
	t.Helper()
	s, err := runtoken.NewSigner([]byte("0123456789abcdef0123456789abcdef"), ttl)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func signer(t testing.TB) *runtoken.Signer { return signerFor(t, tokenTTL) }

func service(t testing.TB, w orchestrator.Worker) *orchestrator.Service {
	t.Helper()
	return serviceWith(t, w, func(*orchestrator.Config) {})
}

// serviceWith is service with the config adjusted (for example the counterfactual flag).
func serviceWith(t testing.TB, w orchestrator.Worker, adjust func(*orchestrator.Config)) *orchestrator.Service {
	t.Helper()
	cfg := config(t)
	adjust(&cfg)
	s, err := orchestrator.New(env.DB, w, signer(t), clock.NewFixed(wallNow), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// scene is one account with a fresh pending run (WP8's output) and the people and evidence candidates may use.
type scene struct {
	RunID, AccountID, Activity string
	EventTime                  time.Time
	Contact1, Contact2         string // people of the account
	Contact3, Rep              string
}

func newScene(t testing.TB) scene {
	t.Helper()
	world := ctxfixture.Get(t, env.DB)
	s := scene{AccountID: world.AccountA}
	s.RunID = ctxfixture.FreshRun(t, env.DB, s.AccountID, "pending")
	if _, err := env.DB.Exec(`INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status)
 SELECT $1::uuid, ord, step, 'dry_run', 'pending' FROM unnest(ARRAY['build_context','draft','crm_intent','await_human','execute']) WITH ORDINALITY AS u(step, ord)`, s.RunID); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.QueryRow(`SELECT trigger_activity_ids[1]::text FROM agent_runs WHERE id = $1::uuid`, s.RunID).Scan(&s.Activity); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.QueryRow(`SELECT occurred_at FROM activities WHERE id = $1::uuid`, s.Activity).Scan(&s.EventTime); err != nil {
		t.Fatal(err)
	}
	s.EventTime = s.EventTime.UTC()
	ids := col(t, `SELECT id::text FROM people WHERE account_id = $1::uuid AND merged_into IS NULL ORDER BY id LIMIT 3`, s.AccountID)
	if len(ids) < 3 {
		t.Fatalf("the sample account has %d people, need 3", len(ids))
	}
	s.Contact1, s.Contact2, s.Contact3 = ids[0], ids[1], ids[2]
	s.Rep = col(t, `SELECT id::text FROM people WHERE kind = 'employee' AND internal_only = false ORDER BY id LIMIT 1`)[0]
	return s
}

func col(t testing.TB, q string, args ...any) []string {
	t.Helper()
	rows, err := env.DB.Query(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func scalar(t testing.TB, q string, args ...any) string {
	t.Helper()
	var v sql.NullString
	if err := env.DB.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v.String
}

func count(t testing.TB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := env.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// ---- the fake worker ------------------------------------------------------------------------------

// fakeWorker is a scripted worker: it counts calls, can fail them and can run a hook inside generation.
type fakeWorker struct {
	mu         sync.Mutex
	sc         scene
	strategies int
	judges     []workerclient.JudgeRequest
	revises    []workerclient.ReviseRequest
	requests   []workerclient.StrategiesRequest

	build     func(call int) []workerclient.Candidate // the candidates of the nth Strategies call
	strErr    func(call int) error
	judgeErr  func(req workerclient.JudgeRequest) error
	judgeItem func(req workerclient.JudgeRequest) []workerclient.BundleItem
	revise    func(req workerclient.ReviseRequest) (workerclient.Candidate, error)
	onGen     func() // runs inside Strategies, while the run is context_built
	ids       map[string]string
}

// idFor gives a strategy a stable candidate id for this fake (the table keys candidates by that id).
func (f *fakeWorker) idFor(strategyType string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ids == nil {
		f.ids = map[string]string{}
	}
	if f.ids[strategyType] == "" {
		f.ids[strategyType] = newUUID()
	}
	return f.ids[strategyType]
}

func newFake(sc scene) *fakeWorker {
	f := &fakeWorker{sc: sc}
	f.build = func(int) []workerclient.Candidate { return f.threeCandidates() }
	return f
}

func (f *fakeWorker) Strategies(_ context.Context, req workerclient.StrategiesRequest) (workerclient.StrategiesResponse, error) {
	f.mu.Lock()
	f.strategies++
	call := f.strategies
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if f.onGen != nil {
		f.onGen()
	}
	if f.strErr != nil {
		if err := f.strErr(call); err != nil {
			return workerclient.StrategiesResponse{}, err
		}
	}
	return workerclient.StrategiesResponse{Candidates: f.build(call), Model: "fake/strategist"}, nil
}

func (f *fakeWorker) Judge(_ context.Context, req workerclient.JudgeRequest) (workerclient.JudgeResponse, error) {
	f.mu.Lock()
	f.judges = append(f.judges, req)
	f.mu.Unlock()
	if f.judgeErr != nil {
		if err := f.judgeErr(req); err != nil {
			return workerclient.JudgeResponse{}, err
		}
	}
	if f.judgeItem != nil {
		return workerclient.JudgeResponse{Items: f.judgeItem(req), Model: "fake/judge"}, nil
	}
	return workerclient.JudgeResponse{Items: []workerclient.BundleItem{semantic(req, "buyer_readiness", "pass", false, ""),
		notRelevant("channel_appropriateness")}, Model: "fake/judge"}, nil
}

func (f *fakeWorker) Revise(_ context.Context, req workerclient.ReviseRequest) (workerclient.ReviseResponse, error) {
	f.mu.Lock()
	f.revises = append(f.revises, req)
	f.mu.Unlock()
	if f.revise == nil {
		return workerclient.ReviseResponse{}, fmt.Errorf("no revision scripted")
	}
	c, err := f.revise(req)
	return workerclient.ReviseResponse{Candidate: c, Model: "fake/reviser"}, err
}

// ---- candidates and eval items ---------------------------------------------------------------------

func (f *fakeWorker) candidate(rank int, strategyType, class, action string, to, cc []string, body string) workerclient.Candidate {
	subject := "Re: next steps"
	recips := func(ids []string, role string) []workerclient.Recipient {
		out := []workerclient.Recipient{}
		for _, id := range ids {
			out = append(out, workerclient.Recipient{PersonID: id, Role: role, Why: "the " + role + " of this strategy"})
		}
		return out
	}
	channel := "email"
	if action == "internal_note" {
		channel = "slack"
	}
	return workerclient.Candidate{
		CandidateID: f.idFor(strategyType), StrategyType: strategyType,
		Title: "Strategy " + strategyType, Description: "One line of intent for " + strategyType, Ranking: rank, PreferredByAgent: rank == 1,
		Rationale: "Plausible for the current state: " + strategyType, StateRefs: []string{"stage"},
		EvidenceRefs: []workerclient.EvidenceRef{{ActivityID: f.sc.Activity}}, KnowledgeRefs: []string{}, ActionType: action,
		ActionClass: class, FiveQuestions: workerclient.FiveQuestions{WhatChanged: "A new message arrived.", WhyStateChanged: "The customer wrote first-party.",
			WhatRemainsUnknown: "Their timing.", PriorKnowledgeApplies: "None applies.", WhyNextAction: "It moves the account forward."},
		To: recips(to, "to"), CC: recips(cc, "cc"), Subject: &subject,
		FullActionArtifact: workerclient.Artifact{Channel: channel, Subject: &subject, Body: body}, Preview: body,
	}
}

func (f *fakeWorker) threeCandidates() []workerclient.Candidate {
	return []workerclient.Candidate{
		f.candidate(1, "send_context_and_wait", "REPLY", "send_email", []string{f.sc.Contact1}, []string{f.sc.Contact2},
			"Thanks for the note. Here is the context you asked about; tell us when your team has read it."),
		f.candidate(2, "bring_in_technical_lead", "MEETING", "schedule_meeting", []string{f.sc.Contact1, f.sc.Contact3}, nil,
			"Could our solutions engineer join a short walkthrough with your engineers about integration details?"),
		f.candidate(3, "ask_internal_owner", "ASK_RESEARCH", "internal_note", []string{f.sc.Rep}, nil,
			"Please check with the account owner which regions the customer plans to roll out first."),
	}
}

func semantic(req workerclient.JudgeRequest, evalType, verdict string, blocking bool, correction string) workerclient.BundleItem {
	r := deterministic.EvalResult{ID: newUUID(), AgentRunID: req.RunID, DraftIndex: req.DraftIndex, EvalType: deterministic.EvalType(evalType),
		EvalVersion: evalType + ":v1", Kind: "semantic", Verdict: verdict, Diagnostics: []string{}, Blocking: blocking,
		Reason: "scripted " + verdict, StateRefs: []string{"stage"}, ActivityRefs: []string{}, EvidenceRefs: []deterministic.EvidenceRef{},
		KnowledgeRefs: []string{}, EvidenceClass: "methodology", CreatedAt: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
		JudgedObject: deterministic.JudgedObject{Type: "StrategyCandidate", ID: req.Candidate.CandidateID},
		SpanID:       deterministic.SpanID(deterministic.SpanCandidates, req.RunID)}
	model := "fake/judge"
	r.Model = &model
	if correction != "" {
		r.SuggestedCorrection = &correction
	}
	raw, _ := json.Marshal(r)
	return workerclient.BundleItem{EvalType: evalType, RelevanceReason: "routed for this candidate", Verdict: verdict, Result: raw}
}

func notRelevant(evalType string) workerclient.BundleItem {
	return workerclient.BundleItem{EvalType: evalType, RelevanceReason: "does not apply to this action", Verdict: "not_relevant", Result: json.RawMessage("null")}
}

func newUUID() string {
	var id string
	if err := env.DB.QueryRow(`SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		panic(err)
	}
	return id
}
