package orchestrator

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
)

var (
	// ErrInProgress: another caller holds this run's draft step right now (and its lease has not expired).
	ErrInProgress = errors.New("orchestrator: the run is being processed by another caller")
	// ErrNotRunnable: the run does not exist, is not a pending/context_built run, or already ended without a set.
	ErrNotRunnable = errors.New("orchestrator: the run cannot be run")
	// ErrLiveRun: the orchestrator builds dry runs only (contract invariant I6: nothing reaches a sender).
	ErrLiveRun = errors.New("orchestrator: only dry_run runs are orchestrated")
)

// TransientError is a failure a later Resume can recover from: the run stays open and resumable.
type TransientError struct {
	Phase  string
	Reason string
	Err    error
}

func (e *TransientError) Error() string {
	return fmt.Sprintf("orchestrator: transient failure in %s (run stays resumable): %s", e.Phase, e.Reason)
}

// Unwrap exposes the cause.
func (e *TransientError) Unwrap() error { return e.Err }

// PermanentError is a failure no retry can fix: the run is marked failed with Reason and the account is freed.
type PermanentError struct {
	Phase  string
	Reason string
	Err    error
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("orchestrator: permanent failure in %s (run failed): %s", e.Phase, e.Reason)
}

// Unwrap exposes the cause.
func (e *PermanentError) Unwrap() error { return e.Err }

// IsTransient reports whether err left the run resumable.
func IsTransient(err error) bool {
	var t *TransientError
	return errors.As(err, &t)
}

// IsPermanent reports whether err marked the run failed.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

func transient(phase string, cause error) error {
	return &TransientError{Phase: phase, Reason: cause.Error(), Err: cause}
}

func permanent(phase string, cause error) error {
	return &PermanentError{Phase: phase, Reason: cause.Error(), Err: cause}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validUUID(s string) bool { return uuidPattern.MatchString(s) }

// newID returns a random version 4 UUID.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("orchestrator: no randomness: " + err.Error())
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
