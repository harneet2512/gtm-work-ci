package slacksurface

import (
	"context"
	"errors"
)

// Errors a Core implementation maps from the API's status codes.
var (
	ErrNotFound = errors.New("slacksurface: not found")
	// ErrNotReady means core has not produced the object yet (strategies_not_ready,
	// inference_not_ready).
	ErrNotReady = errors.New("slacksurface: not ready")
	// ErrConflict means a 409. Use errors.As with *ConflictError for the code and the existing record.
	ErrConflict = errors.New("slacksurface: conflict")
)

// Codes of the 409 responses (contracts/openapi/core.yaml).
const (
	CodeSelectionLocked = "selection_locked" // a different candidate was submitted after the choice
	CodeAlreadyDecided  = "already_decided"  // send or discard was already recorded
	CodeNoChoice        = "no_choice"        // send before any choice
)

// ConflictError is a 409. Decision is the existing record when core returned one with the conflict.
type ConflictError struct {
	Code     string
	Decision *HumanStrategyDecision
}

func (e *ConflictError) Error() string        { return "slacksurface: conflict: " + e.Code }
func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// AmbiguousError is a failure after which the action may or may not have been applied: a timeout, a
// dropped connection, an undecodable answer or a 5xx. The caller must re-read before telling the user
// anything about what changed.
type AmbiguousError struct{ Err error }

func (e *AmbiguousError) Error() string { return "slacksurface: outcome unknown: " + e.Err.Error() }
func (e *AmbiguousError) Unwrap() error { return e.Err }

// RefusedError is a 422: policy refused the action (for example a blocking eval is unresolved).
type RefusedError struct{ Message string }

func (e *RefusedError) Error() string { return "slacksurface: refused: " + e.Message }

// Core is everything the Slack surface needs from the core API. It is the same set of operations a
// web surface uses (HAR-129 section 15); Slack adds no logic of its own. Decisions made here differ
// from web decisions only in Surface and actor_label.
type Core interface {
	GetBIUpdate(ctx context.Context, accountID string) (BusinessIntelligenceUpdate, error)
	GetAccountName(ctx context.Context, accountID string) (string, error)
	// People returns the account's people, to show recipients (the contract carries person ids only)
	// and to resolve edited To/CC text back to person ids.
	People(ctx context.Context, accountID string) (Directory, error)
	GetStrategies(ctx context.Context, runID string) (RunStrategies, error)
	// GetStrategyDecision returns ErrNotFound until the human has chosen.
	GetStrategyDecision(ctx context.Context, runID string) (HumanStrategyDecision, error)
	RecordStrategyDecision(ctx context.Context, runID string, req StrategyDecisionRequest) (HumanStrategyDecision, error)
	SendRun(ctx context.Context, runID string, req SendRequest) (HumanStrategyDecision, error)
	// GetJudgmentInference returns ErrNotReady until core has inferred the judgment.
	GetJudgmentInference(ctx context.Context, episodeID string) (JudgmentInference, error)
	SubmitJudgmentVerdict(ctx context.Context, episodeID string, req VerdictRequest) (JudgmentInference, error)
}
