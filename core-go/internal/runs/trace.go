package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// Trace is a run followed back to what caused it: the evaluation that made it eligible, the signals and
// state diff behind that, the state version it read and the activities that triggered it ("why did the
// account agent wake up?", HAR-129). ContextRefs are the run's own context pulls (context_access_log).
type Trace struct {
	RunID        string
	AccountID    string
	Mode         string
	Status       string
	StateVersion *int
	Evaluation   TraceEvaluation
	Diff         *TraceDiff
	Signals      []TraceSignal
	Activities   []TraceActivity
	Steps        []TraceStep
	ContextRefs  []ContextRef
	// EvidenceRefs is every evidence reference of the diff's changes and the signals, once each.
	EvidenceRefs []reducer.EvidenceRef
}

// TraceEvaluation is the eligibility decision.
type TraceEvaluation struct {
	ID          string
	ReasonCodes []string
	Explanation string
	EvaluatedAt time.Time
}

// TraceDiff is the state diff that was evaluated.
type TraceDiff struct {
	ID          string
	FromVersion int
	ToVersion   int
	IsMaterial  bool
	Changes     json.RawMessage
	ActivityIDs []string
}

// TraceSignal is one signal behind the evaluation.
type TraceSignal struct {
	ID         string
	Type       string
	Rule       string
	OccurredAt time.Time
	// EvidenceRefs are the signal's evidence.
	EvidenceRefs []reducer.EvidenceRef
}

// TraceActivity is one trigger activity.
type TraceActivity struct {
	ID         string
	Type       string
	OccurredAt time.Time
}

// TraceStep is one typed step.
type TraceStep struct {
	Seq              int
	Step             string
	Status           string
	ExternalEffectID string
}

// ContextRef is one logged context pull.
type ContextRef struct {
	AccessID int64
	Tool     string
	Bytes    int
}

// LoadTrace reads the trace of a run.
func LoadTrace(ctx context.Context, db claimstore.DB, runID string) (Trace, error) {
	t := Trace{RunID: runID}
	var activityIDs, signalIDs, reasons []byte
	var diffID sql.NullString
	var version sql.NullInt32
	err := db.QueryRowContext(ctx, `
SELECT r.account_id::text, r.run_mode, r.status, r.state_version, r.trigger_evaluation_id::text, to_jsonb(r.trigger_activity_ids),
       to_jsonb(e.reason_codes), COALESCE(e.explanation, ''), e.evaluated_at, to_jsonb(e.signal_ids), e.state_diff_id::text
  FROM agent_runs r JOIN trigger_evaluations e ON e.id = r.trigger_evaluation_id WHERE r.id = $1::uuid`, runID).Scan(
		&t.AccountID, &t.Mode, &t.Status, &version, &t.Evaluation.ID, &activityIDs,
		&reasons, &t.Evaluation.Explanation, &t.Evaluation.EvaluatedAt, &signalIDs, &diffID)
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("runs: run %s does not exist", runID)
	}
	if err != nil {
		return t, fmt.Errorf("runs: read run %s: %w", runID, err)
	}
	if version.Valid {
		v := int(version.Int32)
		t.StateVersion = &v
	}
	t.Evaluation.EvaluatedAt = t.Evaluation.EvaluatedAt.UTC()
	if err := json.Unmarshal(reasons, &t.Evaluation.ReasonCodes); err != nil {
		return t, fmt.Errorf("runs: decode reasons: %w", err)
	}
	if diffID.Valid {
		if t.Diff, err = loadDiff(ctx, db, diffID.String); err != nil {
			return t, err
		}
	}
	sigIDs, err := jsonStrings(signalIDs)
	if err != nil {
		return t, err
	}
	actIDs, err := jsonStrings(activityIDs)
	if err != nil {
		return t, err
	}
	if t.Signals, err = loadSignals(ctx, db, sigIDs); err != nil {
		return t, err
	}
	if t.Activities, err = loadActivities(ctx, db, actIDs); err != nil {
		return t, err
	}
	if t.Steps, err = loadSteps(ctx, db, runID); err != nil {
		return t, err
	}
	if t.ContextRefs, err = loadContextRefs(ctx, db, runID); err != nil {
		return t, err
	}
	t.EvidenceRefs, err = collectEvidence(t)
	return t, err
}
