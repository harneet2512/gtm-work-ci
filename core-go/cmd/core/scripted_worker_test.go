package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// providerDown is what a worker answers when the model provider refuses calls (claims.IsProviderUnavailable).
type providerDown struct{}

func (providerDown) Error() string             { return "worker 424 provider_unavailable_nonretryable" }
func (providerDown) ProviderUnavailable() bool { return true }

// scriptedWorker is the model worker as a Go double: no network, no model. It reads the run it is asked about from
// the database (the people of the account, the trigger activity), answers with three distinct candidates, and
// judges each one as passing. Hooks let a test hold or fail a call.
type scriptedWorker struct {
	mu         sync.Mutex
	strategies int
	judges     int
	revises    int
	requests   []workerclient.StrategiesRequest
	ids        map[string]string

	onStrategies func(call int) error // runs first in Strategies; a non-nil error is the answer
	onJudge      func(call int)       // runs first in Judge
}

func (w *scriptedWorker) counts() (strategies, judges, revises int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.strategies, w.judges, w.revises
}

func (w *scriptedWorker) Strategies(_ context.Context, req workerclient.StrategiesRequest) (workerclient.StrategiesResponse, error) {
	w.mu.Lock()
	w.strategies++
	call := w.strategies
	w.requests = append(w.requests, req)
	hook := w.onStrategies
	w.mu.Unlock()
	if hook != nil {
		if err := hook(call); err != nil {
			return workerclient.StrategiesResponse{}, err
		}
	}
	cands, err := w.candidates(req)
	if err != nil {
		return workerclient.StrategiesResponse{}, err
	}
	return workerclient.StrategiesResponse{Candidates: cands, Model: "scripted/strategist"}, nil
}

func (w *scriptedWorker) Judge(_ context.Context, req workerclient.JudgeRequest) (workerclient.JudgeResponse, error) {
	w.mu.Lock()
	w.judges++
	call := w.judges
	hook := w.onJudge
	w.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	return workerclient.JudgeResponse{Model: "scripted/judge", Items: []workerclient.BundleItem{
		semanticPass(req, "buyer_readiness"), notRelevant("channel_appropriateness")}}, nil
}

func (w *scriptedWorker) Revise(context.Context, workerclient.ReviseRequest) (workerclient.ReviseResponse, error) {
	w.mu.Lock()
	w.revises++
	w.mu.Unlock()
	return workerclient.ReviseResponse{}, fmt.Errorf("no revision is scripted: every candidate passes")
}

// candidates builds the three strategies from the account's real people and the run's trigger activity.
func (w *scriptedWorker) candidates(req workerclient.StrategiesRequest) ([]workerclient.Candidate, error) {
	contacts, err := column(`SELECT id::text FROM people WHERE account_id = $1::uuid AND merged_into IS NULL ORDER BY primary_email`, req.AccountID)
	if err != nil || len(contacts) < 3 {
		return nil, fmt.Errorf("the account has %d people, need 3: %v", len(contacts), err)
	}
	rep, err := column(`SELECT id::text FROM people WHERE kind = 'employee' AND internal_only = false ORDER BY id LIMIT 1`)
	if err != nil || len(rep) == 0 {
		return nil, fmt.Errorf("no rep: %v", err)
	}
	activity := req.TriggerContext.TriggerActivityIDs[0]
	return []workerclient.Candidate{
		w.candidate(activity, 1, "send_context_and_wait", "REPLY", "send_email", contacts[:1], contacts[1:2],
			"Thanks for the note. Here is the context you asked about; tell us when your team has read it."),
		w.candidate(activity, 2, "bring_in_technical_lead", "MEETING", "schedule_meeting", []string{contacts[0], contacts[2]}, nil,
			"Could our solutions engineer join a short walkthrough with your engineers about integration details?"),
		w.candidate(activity, 3, "ask_internal_owner", "ASK_RESEARCH", "internal_note", rep[:1], nil,
			"Please check with the account owner which regions the customer plans to roll out first."),
	}, nil
}

func (w *scriptedWorker) idFor(strategyType string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ids == nil {
		w.ids = map[string]string{}
	}
	if w.ids[strategyType] == "" {
		w.ids[strategyType] = newUUID()
	}
	return w.ids[strategyType]
}

func (w *scriptedWorker) candidate(activity string, rank int, strategyType, class, action string, to, cc []string, body string) workerclient.Candidate {
	subject := "Re: next steps"
	recipients := func(ids []string, role string) []workerclient.Recipient {
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
		CandidateID: w.idFor(strategyType), StrategyType: strategyType, Title: "Strategy " + strategyType,
		Description: "One line of intent for " + strategyType, Ranking: rank, PreferredByAgent: rank == 1,
		Rationale: "Plausible for the current state: " + strategyType, StateRefs: []string{"stage"},
		EvidenceRefs: []workerclient.EvidenceRef{{ActivityID: activity}}, KnowledgeRefs: []string{}, ActionType: action, ActionClass: class,
		FiveQuestions: workerclient.FiveQuestions{WhatChanged: "A new message arrived.", WhyStateChanged: "The customer wrote first-party.",
			WhatRemainsUnknown: "Their timing.", PriorKnowledgeApplies: "None applies.", WhyNextAction: "It moves the account forward."},
		To: recipients(to, "to"), CC: recipients(cc, "cc"), Subject: &subject,
		FullActionArtifact: workerclient.Artifact{Channel: channel, Subject: &subject, Body: body}, Preview: body,
	}
}

func semanticPass(req workerclient.JudgeRequest, evalType string) workerclient.BundleItem {
	r := deterministic.EvalResult{ID: newUUID(), AgentRunID: req.RunID, DraftIndex: req.DraftIndex, EvalType: deterministic.EvalType(evalType),
		EvalVersion: evalType + ":v1", Kind: "semantic", Verdict: "pass", Diagnostics: []string{}, Reason: "scripted pass",
		StateRefs: []string{}, ActivityRefs: []string{}, EvidenceRefs: []deterministic.EvidenceRef{}, KnowledgeRefs: []string{},
		EvidenceClass: "methodology", CreatedAt: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)}
	model := "scripted/judge"
	r.Model = &model
	raw, _ := json.Marshal(r)
	return workerclient.BundleItem{EvalType: evalType, RelevanceReason: "routed for this candidate", Verdict: "pass", Result: raw}
}

func notRelevant(evalType string) workerclient.BundleItem {
	return workerclient.BundleItem{EvalType: evalType, RelevanceReason: "does not apply to this action", Verdict: "not_relevant", Result: json.RawMessage("null")}
}

// column runs a query that returns one text column.
func column(q string, args ...any) ([]string, error) {
	rows, err := env.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
