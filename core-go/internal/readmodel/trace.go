package readmodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// RunStep is agent_run.v1.json steps[].
type RunStep struct {
	Seq              int             `json:"seq"`
	Step             string          `json:"step"`
	Status           string          `json:"status"`
	StartedAt        *time.Time      `json:"started_at"`
	FinishedAt       *time.Time      `json:"finished_at"`
	ExternalEffectID *string         `json:"external_effect_id"`
	Detail           json.RawMessage `json:"detail"`
}

// ContextRef is agent_run.v1.json input_context_refs[].
type ContextRef struct {
	Tool        string          `json:"tool"`
	AccessID    int64           `json:"access_id"`
	ReturnedIDs json.RawMessage `json:"returned_ids"`
	Bytes       int             `json:"bytes"`
	truncated   bool            // not part of agent_run.v1.json; reported on the trace's packet stub
}

// Run is contracts/schemas/agent_run.v1.json.
type Run struct {
	ID                  string          `json:"id"`
	AccountID           string          `json:"account_id"`
	OpportunityID       *string         `json:"opportunity_id"`
	Workflow            string          `json:"workflow"`
	RunMode             string          `json:"run_mode"`
	Status              string          `json:"status"`
	TriggerEvaluationID string          `json:"trigger_evaluation_id"`
	TriggerActivityIDs  []string        `json:"trigger_activity_ids"`
	CorrelationID       *string         `json:"correlation_id"`
	StateVersion        *int            `json:"state_version"`
	InputContextRefs    []ContextRef    `json:"input_context_refs"`
	Output              json.RawMessage `json:"output"`
	EvidenceRefs        json.RawMessage `json:"evidence_refs"`
	KnowledgeRefsUsed   json.RawMessage `json:"knowledge_refs_used"`
	Model               *string         `json:"model"`
	Steps               []RunStep       `json:"steps"`
	Error               *string         `json:"error"`
	Generation          *Generation     `json:"generation"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// TriggerEvaluation is contracts/schemas/trigger_evaluation.v1.json.
type TriggerEvaluation struct {
	ID          string          `json:"id"`
	AccountID   string          `json:"account_id"`
	Workflow    string          `json:"workflow"`
	Eligible    bool            `json:"eligible"`
	ReasonCodes json.RawMessage `json:"reason_codes"`
	Explanation string          `json:"explanation,omitempty"`
	SignalIDs   json.RawMessage `json:"signal_ids"`
	StateDiffID *string         `json:"state_diff_id"`
	AgentRunID  *string         `json:"agent_run_id"`
	EvaluatedAt time.Time       `json:"evaluated_at"`
}

// Decision is contracts/schemas/human_decision.v1.json.
type Decision struct {
	ID             string          `json:"id"`
	AgentRunID     string          `json:"agent_run_id"`
	Decision       string          `json:"decision"`
	Surface        string          `json:"surface"`
	ActorPersonID  *string         `json:"actor_person_id"`
	ActorLabel     string          `json:"actor_label"`
	EditedArtifact json.RawMessage `json:"edited_artifact"`
	Reason         *string         `json:"reason"`
	CreatedAt      time.Time       `json:"created_at"`
}

// PacketStub is a context_packet.v1.json for a logged pull: the payload is not retained, so the
// items are {id} stubs of the returned ids.
type PacketStub struct {
	AccessID  int64            `json:"access_id"`
	Tool      string           `json:"tool"`
	Items     []map[string]any `json:"items"`
	Truncated bool             `json:"truncated"`
	Bytes     int              `json:"bytes"`
}

// Trace is core.yaml components.schemas.RunTrace: run -> trigger evaluation -> signals -> diff ->
// state -> activities, plus what the agent read and what the human decided.
type Trace struct {
	Run                  Run               `json:"run"`
	TriggerActivities    []Activity        `json:"trigger_activities"`
	CorrelatedActivities []Activity        `json:"correlated_activities"`
	StateBefore          json.RawMessage   `json:"state_before"`
	StateAtRun           json.RawMessage   `json:"state_at_run"`
	StateDiff            *StateDiff        `json:"state_diff"`
	Signals              []Signal          `json:"signals"`
	TriggerEvaluation    TriggerEvaluation `json:"trigger_evaluation"`
	ContextAccesses      []PacketStub      `json:"context_accesses"`
	Decisions            []Decision        `json:"decisions"`
	Placeholders         Placeholders      `json:"placeholders"`
}

// Placeholders are the HAR-97 slots; they stay empty until HAR-97 fills them.
type Placeholders struct {
	EvalRuns          []any `json:"eval_runs"`
	CustomerReactions []any `json:"customer_reactions"`
	KnowledgeUpdates  []any `json:"knowledge_updates"`
}

// Trace returns the full lineage of a run. ErrNotFound: no such run.
func (r *Reader) Trace(ctx context.Context, runID string) (Trace, error) {
	if err := requireUUID("run", runID); err != nil {
		return Trace{}, err
	}
	var tr Trace
	err := r.snapshot(ctx, func(db claimstore.DB) error {
		var err error
		tr, err = buildTrace(ctx, db, runID)
		return err
	})
	return tr, err
}

func buildTrace(ctx context.Context, db claimstore.DB, runID string) (Trace, error) {
	run, err := loadRun(ctx, db, runID)
	if err != nil {
		return Trace{}, err
	}
	tr := Trace{Run: run, Placeholders: Placeholders{EvalRuns: []any{}, CustomerReactions: []any{}, KnowledgeUpdates: []any{}}}
	if tr.TriggerEvaluation, err = loadTrigger(ctx, db, run); err != nil {
		return Trace{}, err
	}
	var signalIDs []string
	if err := json.Unmarshal(tr.TriggerEvaluation.SignalIDs, &signalIDs); err != nil {
		return Trace{}, fmt.Errorf("readmodel: decode signal ids: %w", err)
	}
	if tr.Signals, err = signalsByID(ctx, db, run.AccountID, signalIDs); err != nil {
		return Trace{}, err
	}
	if tr.StateDiff, err = loadRunDiff(ctx, db, run, tr.TriggerEvaluation.StateDiffID); err != nil {
		return Trace{}, err
	}
	if err := loadRunStates(ctx, db, &tr); err != nil {
		return Trace{}, err
	}
	if err := loadRunActivities(ctx, db, &tr); err != nil {
		return Trace{}, err
	}
	tr.ContextAccesses = loadAccessStubs(run.InputContextRefs)
	tr.Decisions, err = loadDecisions(ctx, db, runID)
	return tr, err
}

func loadRun(ctx context.Context, db claimstore.DB, runID string) (Run, error) {
	var run Run
	var ids, output, evidence, knowledge []byte
	err := db.QueryRowContext(ctx, `SELECT id::text, account_id::text, opportunity_id::text, workflow, run_mode, status,
 trigger_evaluation_id::text, to_jsonb(trigger_activity_ids), correlation_id::text, state_version, output,
 COALESCE(output -> 'evidence_refs', '[]'::jsonb), knowledge_refs_used, model, error, created_at, updated_at
 FROM agent_runs WHERE id = $1::uuid`, runID).Scan(&run.ID, &run.AccountID, &run.OpportunityID, &run.Workflow,
		&run.RunMode, &run.Status, &run.TriggerEvaluationID, &ids, &run.CorrelationID, &run.StateVersion, &output,
		&evidence, &knowledge, &run.Model, &run.Error, &run.CreatedAt, &run.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return run, ErrNotFound
	}
	if err != nil {
		return run, fmt.Errorf("readmodel: load run: %w", err)
	}
	if err := json.Unmarshal(ids, &run.TriggerActivityIDs); err != nil {
		return run, fmt.Errorf("readmodel: decode trigger activity ids: %w", err)
	}
	run.Output, run.EvidenceRefs, run.KnowledgeRefsUsed = output, evidence, knowledge
	if len(run.Output) == 0 {
		run.Output = json.RawMessage("null")
	}
	run.CreatedAt, run.UpdatedAt = utc(run.CreatedAt), utc(run.UpdatedAt)
	if run.InputContextRefs, err = loadContextRefs(ctx, db, runID); err != nil {
		return run, err
	}
	if run.Steps, err = loadSteps(ctx, db, runID); err != nil {
		return run, err
	}
	run.Generation, err = loadGeneration(ctx, db, runID, run.Status, run.Error)
	return run, err
}

func loadContextRefs(ctx context.Context, db claimstore.DB, runID string) ([]ContextRef, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, tool, returned_ids, bytes, truncated FROM context_access_log
 WHERE agent_run_id = $1::uuid ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("readmodel: load context log: %w", err)
	}
	defer rows.Close()
	out := []ContextRef{}
	for rows.Next() {
		var c ContextRef
		var ids []byte
		if err := rows.Scan(&c.AccessID, &c.Tool, &ids, &c.Bytes, &c.truncated); err != nil {
			return nil, fmt.Errorf("readmodel: scan context log: %w", err)
		}
		c.ReturnedIDs = ids
		out = append(out, c)
	}
	return out, rows.Err()
}

func loadSteps(ctx context.Context, db claimstore.DB, runID string) ([]RunStep, error) {
	rows, err := db.QueryContext(ctx, `SELECT seq, step, status, started_at, finished_at, external_effect_id, detail
 FROM agent_run_steps WHERE agent_run_id = $1::uuid ORDER BY seq`, runID)
	if err != nil {
		return nil, fmt.Errorf("readmodel: load steps: %w", err)
	}
	defer rows.Close()
	out := []RunStep{}
	for rows.Next() {
		var s RunStep
		var detail []byte
		if err := rows.Scan(&s.Seq, &s.Step, &s.Status, &s.StartedAt, &s.FinishedAt, &s.ExternalEffectID, &detail); err != nil {
			return nil, fmt.Errorf("readmodel: scan step: %w", err)
		}
		s.Detail, s.StartedAt, s.FinishedAt = detail, utcPtr(s.StartedAt), utcPtr(s.FinishedAt)
		out = append(out, s)
	}
	return out, rows.Err()
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func loadAccessStubs(refs []ContextRef) []PacketStub {
	out := make([]PacketStub, 0, len(refs))
	for _, c := range refs {
		var ids []string
		_ = json.Unmarshal(c.ReturnedIDs, &ids)
		items := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			items = append(items, map[string]any{"id": id})
		}
		out = append(out, PacketStub{AccessID: c.AccessID, Tool: c.Tool, Items: items, Truncated: c.truncated, Bytes: c.Bytes})
	}
	return out
}
