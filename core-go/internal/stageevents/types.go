// Package stageevents records and reads what the real pipeline did, stage by stage (HAR-145, "Live Play Event
// N"; contracts/schemas/pipeline_progress.v1.json). A row is written by the code that executes a stage, when the
// stage starts and again when it ends: there are no timers, no placeholder rows and no inference from the
// absence of an error. A stage without a row is `waiting`; a stage whose outcome cannot be established is
// `unknown`. A transport error (a timeout, an unreachable dependency) is a failure_kind, never an eval verdict:
// the judging stage reports it as `unknown`, and is `failed` only when judged results failed.
package stageevents

import "time"

// Stage is one pipeline stage, in canonical (presentation) order.
type Stage string

// The stages. Their order in Order is presentation order; started_at says what really ran first (the graph
// projection, for one, follows the recompute that the state stage waits on).
const (
	Ingest  Stage = "ingest"
	Resolve Stage = "resolve"
	Graph   Stage = "graph"
	State   Stage = "state"
	Decide  Stage = "decide"
	Evals   Stage = "evals"
	Cliff   Stage = "cliff"
)

// Order is the canonical stage order of a progress document.
var Order = []Stage{Ingest, Resolve, Graph, State, Decide, Evals, Cliff}

// Status is a stage's state (pipeline_progress.v1.json stageStatus). Completed means a non-judging stage ran to
// its end; Passed and Warning are eval verdicts and belong to the evals stage alone.
type Status string

// The statuses.
const (
	Waiting Status = "waiting"
	Running Status = "running"
	// Completed: a stage other than evals ran to its end. Says nothing about quality.
	Completed Status = "completed"
	Passed    Status = "passed"
	Warning   Status = "warning"
	Failed    Status = "failed"
	Skipped   Status = "skipped"
	Unknown   Status = "unknown"
)

// FailureKind says why a stage did not complete.
type FailureKind string

// The failure kinds.
const (
	// Transport: a timeout, a cancelled call or an unreachable dependency. The outcome is not known and a retry
	// may succeed. Never an eval verdict.
	Transport FailureKind = "transport"
	// Contract: the stage's input or output violated a contract (a mismatched event, unusable model output).
	Contract FailureKind = "contract"
	// Internal: anything else (a database error, a bug).
	Internal FailureKind = "internal"
)

// Overall is the whole pipeline's state.
type Overall string

// The overall states.
const (
	NotStarted Overall = "not_started"
	InProgress Overall = "running"
	Broken     Overall = "failed"
	Complete   Overall = "complete"
)

// SlackRef is a Cliff message ref; TS is nil while the slot is reserved and the post is not recorded.
type SlackRef struct {
	SubjectID string  `json:"subject_id"`
	Kind      string  `json:"kind"`
	Channel   string  `json:"channel"`
	TS        *string `json:"ts"`
}

// Refs are the rows a stage produced. Only the keys that exist are serialized.
type Refs struct {
	SourceEventID       string     `json:"source_event_id,omitempty"`
	ActivityID          string     `json:"activity_id,omitempty"`
	OpportunityID       string     `json:"opportunity_id,omitempty"`
	AccountChangeID     string     `json:"account_change_id,omitempty"`
	BIUpdateID          string     `json:"bi_update_id,omitempty"`
	StateDiffID         string     `json:"state_diff_id,omitempty"`
	StateVersion        int        `json:"state_version,omitempty"`
	GraphDiffID         int64      `json:"graph_diff_id,omitempty"`
	TriggerEvaluationID string     `json:"trigger_evaluation_id,omitempty"`
	RunID               string     `json:"run_id,omitempty"`
	StrategySetID       string     `json:"strategy_set_id,omitempty"`
	DecisionEpisodeID   string     `json:"decision_episode_id,omitempty"`
	EvalBundleIDs       []string   `json:"eval_bundle_ids,omitempty"`
	OutboxEventIDs      []int64    `json:"outbox_event_ids,omitempty"`
	SlackRefs           []SlackRef `json:"slack_refs,omitempty"`
}

// Scope names the owner of a stage row: the manifest whose Play ran it, or the run it belongs to. AccountID is
// always required.
type Scope struct {
	ManifestID string
	RunID      string
	AccountID  string
}

// Outcome is what a finished stage reports.
type Outcome struct {
	Refs          Refs
	EvalResultIDs []string
	Detail        string
}

// StageDoc is one stage of a progress document (pipeline_progress.v1.json stage).
type StageDoc struct {
	Stage         Stage        `json:"stage"`
	Status        Status       `json:"status"`
	Attempt       int          `json:"attempt"`
	StartedAt     *time.Time   `json:"started_at"`
	EndedAt       *time.Time   `json:"ended_at"`
	DurationMS    *int64       `json:"duration_ms"`
	Refs          Refs         `json:"refs"`
	EvalResultIDs []string     `json:"eval_result_ids"`
	FailureKind   *FailureKind `json:"failure_kind"`
	Detail        *string      `json:"detail"`
	Seq           *int64       `json:"seq"`
	UpdatedAt     *time.Time   `json:"updated_at"`
}

// Progress is pipeline_progress.v1.json.
type Progress struct {
	Scope       string     `json:"scope"`
	ManifestID  *string    `json:"manifest_id"`
	RunID       *string    `json:"run_id"`
	AccountID   string     `json:"account_id"`
	Overall     Overall    `json:"overall"`
	Stages      []StageDoc `json:"stages"`
	GeneratedAt time.Time  `json:"generated_at"`
}
