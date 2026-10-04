package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
)

const (
	maxWhyNow   = 1000
	maxAction   = 300
	maxWhy      = 500
	defaultMove = "send_email" // the post-interaction follow-up workflow's default move; the agent may choose another
)

// Guidance is contracts/schemas/decision_guidance.v1.json: company knowledge converted into a recommendation for
// this account now. It is a pure function of (state, signals, transition, knowledge as of the replay clock).
type Guidance struct {
	ID                  string            `json:"id"`
	AccountID           string            `json:"account_id"`
	AgentRunID          string            `json:"agent_run_id"`
	StateVersion        int               `json:"state_version"`
	RecommendedAction   string            `json:"recommended_action"`
	NotRecommended      []notRecommended  `json:"not_recommended"`
	WhyNow              string            `json:"why_now"`
	WhoToInvolve        []personWhy       `json:"who_to_involve"`
	WhoNotToInvolve     []personWhy       `json:"who_not_to_involve"`
	SupportingKnowledge []knowledge.Entry `json:"supporting_knowledge"`
	CreatedAt           time.Time         `json:"created_at"`
}

type notRecommended struct {
	Action string `json:"action"`
	Why    string `json:"why"`
}

type personWhy struct {
	PersonID string `json:"person_id"`
	Why      string `json:"why"`
}

// Attribution is the influence record of the run's knowledge (E7): retrieved -> applicable -> used. Retrieved is
// everything that existed at the replay clock; applicable passed its conditions and no exception fired; an
// exception-blocked entry matched but an exception overrode it; used is filled at publish from what the final
// candidates cite.
type Attribution struct {
	AsOf             time.Time `json:"as_of"`
	Retrieved        []string  `json:"retrieved"`
	Applicable       []string  `json:"applicable"`
	ExceptionBlocked []string  `json:"exception_blocked"`
	Used             []string  `json:"used"`
}

// guidanceSet is the persisted guidance with the knowledge it was built from.
type guidanceSet struct {
	Doc         Guidance
	Raw         json.RawMessage
	Knowledge   []knowledge.Knowledge
	Applied     map[string]bool // applies and no triggered exception: what a candidate may cite (I4)
	Attribution Attribution
}

func appliedSet(entries []knowledge.Entry) map[string]bool {
	out := map[string]bool{}
	for _, e := range entries {
		triggered := slices.ContainsFunc(e.ExceptionsChecked, func(x knowledge.ExceptionCheck) bool { return x.Triggered })
		if e.Applies && !triggered {
			out[e.KnowledgeID] = true
		}
	}
	return out
}

// buildGuidance converts the matcher's verdicts into DecisionGuidance. why_now and not_recommended carry what the
// applying knowledge says (its summary, do and dont); there is no model here, so no one is named for involvement.
func buildGuidance(id, runID, accountID string, stateVersion int, now time.Time, ks []knowledge.Knowledge, results []knowledge.Result) Guidance {
	byID := map[string]knowledge.Knowledge{}
	for _, k := range ks {
		byID[k.ID] = k
	}
	g := Guidance{ID: id, AccountID: accountID, AgentRunID: runID, StateVersion: stateVersion, RecommendedAction: defaultMove,
		NotRecommended: []notRecommended{}, WhoToInvolve: []personWhy{}, WhoNotToInvolve: []personWhy{},
		SupportingKnowledge: make([]knowledge.Entry, 0, len(results)), CreatedAt: now.UTC()}
	var lines []string
	for _, r := range results {
		g.SupportingKnowledge = append(g.SupportingKnowledge, r.Entry)
		if r.Label != knowledge.LabelApplies {
			continue
		}
		k := byID[r.Entry.KnowledgeID]
		line := k.Title + ": " + k.Guidance.Summary
		if len(k.Guidance.Do) > 0 {
			line += " Do: " + strings.Join(k.Guidance.Do, "; ") + "."
		}
		lines = append(lines, line)
		for _, d := range k.Guidance.Dont {
			g.NotRecommended = append(g.NotRecommended, notRecommended{Action: clip(d, maxAction), Why: clip("Company knowledge: "+k.Title, maxWhy)})
		}
	}
	if len(lines) == 0 {
		g.WhyNow = "No company knowledge applies to this situation; decide from the current state."
	} else {
		g.WhyNow = clip("Applicable company knowledge. "+strings.Join(lines, " "), maxWhyNow)
	}
	return g
}

func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}

func attributionOf(asOf time.Time, results []knowledge.Result) Attribution {
	a := Attribution{AsOf: asOf.UTC(), Retrieved: []string{}, Applicable: []string{}, ExceptionBlocked: []string{}, Used: []string{}}
	for _, r := range results {
		a.Retrieved = append(a.Retrieved, r.Entry.KnowledgeID)
		switch r.Label {
		case knowledge.LabelApplies:
			a.Applicable = append(a.Applicable, r.Entry.KnowledgeID)
		case knowledge.LabelExceptionTriggered:
			a.ExceptionBlocked = append(a.ExceptionBlocked, r.Entry.KnowledgeID)
		}
	}
	return a
}

// match retrieves the knowledge that existed at the replay clock and matches it against the situation. The as-of
// filter lives in the store (ListApplicableAsOf), not here: nothing is filtered after the fact.
func (s *Service) match(ctx context.Context, w world) ([]knowledge.Knowledge, []knowledge.Result, error) {
	ks, err := knowledgestore.ListApplicableAsOf(ctx, s.db, s.cfg.Knowledge, w.EventTime)
	if err != nil {
		return nil, nil, transient("guidance", err)
	}
	results, err := knowledge.MatchAll(ks, w.Situation)
	if err != nil {
		return nil, nil, permanent("guidance", fmt.Errorf("match knowledge: %w", err))
	}
	return ks, results, nil
}

// ensureGuidance persists DecisionGuidance before anything is generated (invariant: guidance precedes drafting)
// and moves the run to context_built; a resume reloads it and rebuilds the same knowledge view.
func (s *Service) ensureGuidance(ctx context.Context, run runRow, w world) (guidanceSet, error) {
	ks, results, err := s.match(ctx, w)
	if err != nil {
		return guidanceSet{}, err
	}
	if run.GuidanceID != "" {
		return s.loadGuidance(ctx, run, ks, results, w)
	}
	g := buildGuidance(newID(), run.ID, run.AccountID, w.State.Version, s.clk.Now(), ks, results)
	raw, err := json.Marshal(g)
	if err != nil {
		return guidanceSet{}, permanent("guidance", err)
	}
	attr := attributionOf(w.EventTime, results)
	detail, _ := json.Marshal(map[string]any{"knowledge_attribution": attr, "replay_clock": w.EventTime.UTC()})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return guidanceSet{}, transient("guidance", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_guidance (id, account_id, agent_run_id, state_version, guidance)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::jsonb)`, g.ID, run.AccountID, run.ID, g.StateVersion, string(raw)); err != nil {
		return guidanceSet{}, transient("guidance", fmt.Errorf("persist guidance: %w", err))
	}
	res, err := tx.ExecContext(ctx, `UPDATE agent_runs SET decision_guidance_id = $2::uuid, status = 'context_built', state_version = $3,
 updated_at = now() WHERE id = $1::uuid AND status IN ('pending', 'context_built')`, run.ID, g.ID, g.StateVersion)
	if err != nil {
		return guidanceSet{}, transient("guidance", fmt.Errorf("move run to context_built: %w", err))
	}
	if n, _ := res.RowsAffected(); n != 1 {
		// another caller finished or failed the run since it was loaded: nothing here may continue it
		return guidanceSet{}, fmt.Errorf("%w: run %s left pending/context_built while its guidance was persisted", ErrNotRunnable, run.ID)
	}
	res, err = tx.ExecContext(ctx, `UPDATE agent_run_steps SET status = 'succeeded', started_at = now(), finished_at = now(), detail = $2::jsonb
 WHERE agent_run_id = $1::uuid AND step = 'build_context'`, run.ID, string(detail))
	if err != nil {
		return guidanceSet{}, transient("guidance", fmt.Errorf("record build_context: %w", err))
	}
	if n, _ := res.RowsAffected(); n != 1 {
		// the run has no build_context step to record the attribution on: the run is malformed, retrying cannot fix it
		return guidanceSet{}, permanent("guidance", fmt.Errorf("run %s has no build_context step (%d rows)", run.ID, n))
	}
	if err := tx.Commit(); err != nil {
		return guidanceSet{}, transient("guidance", err)
	}
	return guidanceSet{Doc: g, Raw: raw, Knowledge: ks, Applied: appliedSet(g.SupportingKnowledge), Attribution: attr}, nil
}

// loadGuidance is the resume path: the stored document is the truth (it is what the worker was and will be told).
func (s *Service) loadGuidance(ctx context.Context, run runRow, ks []knowledge.Knowledge, results []knowledge.Result, w world) (guidanceSet, error) {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT guidance FROM decision_guidance WHERE id = $1::uuid`, run.GuidanceID).Scan(&raw); err != nil {
		return guidanceSet{}, transient("guidance", fmt.Errorf("load guidance %s: %w", run.GuidanceID, err))
	}
	var g Guidance
	if err := json.Unmarshal(raw, &g); err != nil {
		return guidanceSet{}, permanent("guidance", fmt.Errorf("decode guidance %s: %w", run.GuidanceID, err))
	}
	return guidanceSet{Doc: g, Raw: raw, Knowledge: ks, Applied: appliedSet(g.SupportingKnowledge), Attribution: attributionOf(w.EventTime, results)}, nil
}
