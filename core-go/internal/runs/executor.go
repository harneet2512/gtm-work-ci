package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Errors of the execute step.
var (
	// ErrModeMismatch: the executor's mode is not the run's mode (a live executor never touches a dry run).
	ErrModeMismatch = errors.New("runs: executor mode does not match the run's mode")
	// ErrNotApproved: a live run executes only after a human approved or edited it.
	ErrNotApproved = errors.New("runs: a live run executes only after human approval")
	// ErrLiveNeedsSender: live execution needs a Sender.
	ErrLiveNeedsSender = errors.New("runs: a live executor needs a sender")
	// ErrNotRecordable: a dry run is recorded only from drafted, approved or edited (never over a decision).
	ErrNotRecordable = errors.New("runs: a dry run is recorded only from drafted, approved or edited")
	// ErrAlreadyExecuted: the run's execute step is not pending (it ran, is running, or was recorded).
	ErrAlreadyExecuted = errors.New("runs: the execute step is not pending")
)

// Effect is one external write a run intends: an email to send, a CRM next step to write.
// IdempotencyKey is set by ExecuteStep (run id) so a connector can drop a repeated send.
type Effect struct {
	Kind           string          `json:"kind"` // send_email | crm_next_step | ...
	Target         string          `json:"target"`
	Body           json.RawMessage `json:"body,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
}

// Sender performs an effect in the outside world and returns the connector's id for it.
type Sender interface {
	Send(ctx context.Context, e Effect) (effectID string, err error)
}

// Result is the outcome of executing an effect: the step status and, for a live send, the external id.
type Result struct {
	Status           string // recorded (dry run) | succeeded | failed (live)
	ExternalEffectID string // "" in a dry run
	Detail           map[string]any
}

// Executor executes the final step of a run. The interface is sealed: only the executors of this package
// implement it, so no outside type can claim to be a dry-run executor and still reach a sender.
type Executor interface {
	Mode() string
	Execute(ctx context.Context, e Effect) (Result, error)
	sealed()
}

// NewExecutor returns the executor for a mode. A dry-run executor is built without the sender: the
// type has no field to hold one, so no code path of a dry run can reach an external system. The sender
// argument is ignored for dry_run and required for live.
func NewExecutor(mode string, sender Sender) (Executor, error) {
	switch mode {
	case DryRun:
		return &RecordingExecutor{}, nil
	case Live:
		if sender == nil {
			return nil, ErrLiveNeedsSender
		}
		return liveExecutor{sender: sender}, nil
	}
	return nil, fmt.Errorf("runs: run mode %q is not dry_run or live", mode)
}

// RecordingExecutor is the dry-run executor: it records what would have been sent and nothing else.
type RecordingExecutor struct {
	mu       sync.Mutex
	recorded []Effect
}

func (*RecordingExecutor) sealed() {}

// Mode implements Executor.
func (*RecordingExecutor) Mode() string { return DryRun }

// Execute records the effect. It performs no I/O.
func (r *RecordingExecutor) Execute(_ context.Context, e Effect) (Result, error) {
	r.mu.Lock()
	r.recorded = append(r.recorded, e)
	r.mu.Unlock()
	return Result{Status: "recorded", Detail: map[string]any{"recorded_effect": e}}, nil
}

// Recorded returns a copy of the effects recorded so far.
func (r *RecordingExecutor) Recorded() []Effect {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Effect(nil), r.recorded...)
}

type liveExecutor struct{ sender Sender }

func (liveExecutor) sealed() {}

func (liveExecutor) Mode() string { return Live }

func (l liveExecutor) Execute(ctx context.Context, e Effect) (Result, error) {
	id, err := l.sender.Send(ctx, e)
	if err != nil {
		return Result{Status: "failed", Detail: map[string]any{"error": err.Error()}}, err
	}
	return Result{Status: "succeeded", ExternalEffectID: id, Detail: map[string]any{"effect": e}}, nil
}
