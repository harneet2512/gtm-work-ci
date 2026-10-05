package orchestrator

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// candidateCount is the demo path's set size (HAR-129 section 6).
const candidateCount = 3

var (
	strategyTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// actionClasses maps a decision class to the tool actions that carry it out (strategy_candidate.v1.json allOf).
	actionClasses = map[string][]string{
		"REPLY": {"send_email"}, "MEETING": {"schedule_meeting"}, "SHARE_DOCUMENT": {"share_document"},
		"INTERNAL_TASK": {"internal_note"}, "WAIT": {"wait"}, "NO_ACTION": {"no_action"}, "ASK_RESEARCH": {"internal_note"},
		"CRM_UPDATE": {"internal_note"}, "HUMAN_REVIEW": {"internal_note"}, "UNKNOWN": {"no_action"},
		"EXPANSION_MOTION": {"send_email", "schedule_meeting", "share_document"},
	}
	channels = []string{"email", "slack", "crm_note", "none"}
)

// invalidOutput is a worker answer core refuses to store: untrusted model output is re-validated before any write.
type invalidOutput struct{ Violations []string }

func (e *invalidOutput) Error() string {
	return "the worker's candidates are invalid: " + strings.Join(e.Violations, "; ")
}

func within(s string, lo, hi int) bool { n := utf8.RuneCountInString(s); return n >= lo && n <= hi }

// shapeViolations is the pure part of the validator: count, ranks, preferred flag, field limits, the decision
// class mapping, the five questions, knowledge (I4: only knowledge whose guidance entry applies and has no
// triggered exception) and distinctness (I10). applied is the set of knowledge ids a candidate may cite.
func shapeViolations(cands []workerclient.Candidate, applied map[string]bool) []string {
	if len(cands) != candidateCount {
		return []string{fmt.Sprintf("%d candidates, want exactly %d", len(cands), candidateCount)}
	}
	var out []string
	add := func(i int, format string, a ...any) {
		out = append(out, fmt.Sprintf("candidate %d: "+format, append([]any{i + 1}, a...)...))
	}
	ranks, ids := map[int]bool{}, map[string]bool{}
	shapes := make([]shape, 0, len(cands))
	for i, c := range cands {
		switch {
		case !validUUID(c.CandidateID) || ids[c.CandidateID]:
			add(i, "candidate_id must be a unique uuid")
		case c.Ranking < 1 || c.Ranking > candidateCount || ranks[c.Ranking]:
			add(i, "ranking %d is not a unique rank in 1..%d", c.Ranking, candidateCount)
		}
		ids[c.CandidateID], ranks[c.Ranking] = true, true
		if c.PreferredByAgent != (c.Ranking == 1) {
			add(i, "preferred_by_agent must be true exactly for ranking 1")
		}
		out = append(out, fieldViolations(i, c, applied)...)
		shapes = append(shapes, shape{StrategyType: c.StrategyType, ActionType: c.ActionType, People: distinctPeople(c), Body: c.FullActionArtifact.Body})
	}
	for _, why := range distinctnessViolations(shapes) {
		out = append(out, "not materially distinct: "+why)
	}
	return out
}

func fieldViolations(i int, c workerclient.Candidate, applied map[string]bool) []string {
	var out []string
	add := func(format string, a ...any) {
		out = append(out, fmt.Sprintf("candidate %d: "+format, append([]any{i + 1}, a...)...))
	}
	if !strategyTypePattern.MatchString(c.StrategyType) || len(c.StrategyType) > 80 {
		add("strategy_type %q is not lower snake case of at most 80 characters", c.StrategyType)
	}
	for _, f := range []struct {
		name, v string
		hi      int
	}{{"title", c.Title, 80}, {"description", c.Description, 300}, {"rationale", c.Rationale, 1500}, {"preview", c.Preview, 600}} {
		if !within(f.v, 1, f.hi) {
			add("%s must be 1 to %d characters", f.name, f.hi)
		}
	}
	if types, ok := actionClasses[c.ActionClass]; !ok || !slices.Contains(types, c.ActionType) {
		add("action_class %q cannot be carried out as %q", c.ActionClass, c.ActionType)
	}
	q := c.FiveQuestions
	for name, v := range map[string]string{"what_changed": q.WhatChanged, "why_state_changed": q.WhyStateChanged,
		"what_remains_unknown": q.WhatRemainsUnknown, "prior_knowledge_applies": q.PriorKnowledgeApplies, "why_next_action": q.WhyNextAction} {
		if strings.TrimSpace(v) == "" || !within(v, 1, 600) {
			add("five_questions.%s must be answered (1 to 600 characters)", name)
		}
	}
	for _, r := range c.To {
		if r.Role != "to" || !validUUID(r.PersonID) {
			add("to recipients need a person_id and role 'to'")
			break
		}
	}
	for _, r := range c.CC {
		if r.Role != "cc" || !validUUID(r.PersonID) {
			add("cc recipients need a person_id and role 'cc'")
			break
		}
	}
	art := c.FullActionArtifact
	switch {
	case !slices.Contains(channels, art.Channel) || !within(art.Body, 0, 20000):
		add("full_action_artifact needs a known channel and a body of at most 20000 characters")
	case derefString(c.Subject) != derefString(art.Subject) || (c.Subject == nil) != (art.Subject == nil):
		add("subject must equal full_action_artifact.subject")
	case c.ActionType == "send_email" && (len(c.To) == 0 || art.Channel != "email"):
		add("an email needs a recipient and the email channel")
	}
	if len(c.EvidenceRefs) == 0 {
		add("evidence_refs must cite at least one activity")
	}
	for _, e := range c.EvidenceRefs {
		if !validUUID(e.ActivityID) {
			add("evidence_refs hold a non-uuid activity id")
			break
		}
	}
	seen := map[string]bool{}
	for _, k := range c.KnowledgeRefs {
		if seen[k] || !applied[k] {
			add("knowledge_refs %q is not knowledge whose guidance applies without a triggered exception", k)
		}
		seen[k] = true
	}
	return out
}

// worldViolations checks the candidates against the database (invariants I8 and I12): every recipient is a person
// of the account or an employee, and every cited activity belongs to the account and happened at or before the
// replay clock asOf (evidence from after the event cannot have informed a decision at it).
func (s *Service) worldViolations(ctx context.Context, accountID string, asOf time.Time, cands []workerclient.Candidate) ([]string, error) {
	var people, activities []string
	for _, c := range cands {
		for _, r := range slices.Concat(c.To, c.CC) {
			people = appendUnique(people, r.PersonID)
		}
		for _, e := range c.EvidenceRefs {
			activities = appendUnique(activities, e.ActivityID)
		}
	}
	var out []string
	var known int
	if len(people) > 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM people WHERE id = ANY($1::uuid[]) AND merged_into IS NULL
 AND (kind = 'employee' OR account_id = $2::uuid)`, signalstore.UUIDArray(people), accountID).Scan(&known); err != nil {
			return nil, fmt.Errorf("check recipients: %w", err)
		}
		if known != len(people) {
			out = append(out, "a recipient is not a person of this account or an employee")
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM activities WHERE id = ANY($1::uuid[]) AND account_id = $2::uuid
 AND occurred_at <= $3`, signalstore.UUIDArray(activities), accountID, asOf.UTC()).Scan(&known); err != nil {
		return nil, fmt.Errorf("check evidence: %w", err)
	}
	if known != len(activities) {
		out = append(out, "evidence cites an activity that is not this account's or that happened after the replay clock")
	}
	return out, nil
}

func appendUnique(xs []string, x string) []string {
	if slices.Contains(xs, x) {
		return xs
	}
	return append(xs, x)
}

// validate runs both halves and returns an *invalidOutput, or a database error (transient), or nil.
func (s *Service) validate(ctx context.Context, accountID string, asOf time.Time, cands []workerclient.Candidate, applied map[string]bool) error {
	if v := shapeViolations(cands, applied); len(v) > 0 {
		return &invalidOutput{Violations: v}
	}
	v, err := s.worldViolations(ctx, accountID, asOf, cands)
	if err != nil {
		return err
	}
	if len(v) > 0 {
		return &invalidOutput{Violations: v}
	}
	return nil
}
