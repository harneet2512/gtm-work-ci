// Package providerbreaker is the process-wide circuit breaker around the model worker (HAR-135). After N
// consecutive provider failures (a non-retryable 401/402/403-class refusal, an unreachable worker, a 5xx)
// it opens and every model call is refused locally, so a dead or unfunded key is not hammered. It leaves
// the open state only after a cool-down (one probe call is then allowed through) or on an explicit
// operator Reset.
package providerbreaker

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
)

// Defaults for New.
const (
	DefaultThreshold = 5
	DefaultCooldown  = 5 * time.Minute
)

// State of the breaker.
type State string

// The breaker states.
const (
	Closed   State = "closed"
	Open     State = "open"
	HalfOpen State = "half_open" // cool-down elapsed: calls are allowed; one failure re-opens, one success closes
)

// Status is a point-in-time snapshot (the operator endpoint and metrics read it).
type Status struct {
	State               State      `json:"state"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	Threshold           int        `json:"threshold"`
	CooldownSeconds     float64    `json:"cooldown_seconds"`
	TripsTotal          int64      `json:"trips_total"`
	RejectedTotal       int64      `json:"rejected_total"`
	OpenedAt            *time.Time `json:"opened_at"`
	LastReason          string     `json:"last_reason"`
}

// Breaker is safe for concurrent use.
type Breaker struct {
	mu        sync.Mutex
	clk       clock.Clock
	log       *slog.Logger
	threshold int
	cooldown  time.Duration

	failures int
	opened   bool
	probing  bool // half-open: one probe call is in flight
	openedAt time.Time
	trips    int64
	rejected int64
	reason   string
}

// New returns a closed breaker. Zero threshold/cooldown select the defaults; nil clk is the wall clock.
func New(threshold int, cooldown time.Duration, clk clock.Clock, log *slog.Logger) (*Breaker, error) {
	if threshold < 0 || cooldown < 0 {
		return nil, errors.New("providerbreaker: threshold and cooldown must not be negative")
	}
	if threshold == 0 {
		threshold = DefaultThreshold
	}
	if cooldown == 0 {
		cooldown = DefaultCooldown
	}
	if clk == nil {
		clk = clock.Real{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Breaker{clk: clk, log: log, threshold: threshold, cooldown: cooldown}, nil
}

func (b *Breaker) stateLocked() State {
	switch {
	case !b.opened:
		return Closed
	case b.clk.Now().Sub(b.openedAt) < b.cooldown:
		return Open
	default:
		return HalfOpen
	}
}

// State reports the current state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stateLocked()
}

// Allow reports whether a model call may be made. A refused call is counted. While half-open exactly one
// caller is allowed (the probe) until it reports Success, Failure or ReleaseProbe.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.stateLocked() {
	case Open:
		b.rejected++
		return false
	case HalfOpen:
		if b.probing {
			b.rejected++
			return false
		}
		b.probing = true
	}
	return true
}

// ReleaseProbe ends a probe that produced no verdict on the provider (a request error, a cancelled
// context), so the next caller may probe.
func (b *Breaker) ReleaseProbe() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
}

// Open reports the open state without counting a rejection (for a gate that only peeks).
func (b *Breaker) Open() bool { return b.State() == Open }

// Success records a good call: the failure streak ends and a half-open breaker closes.
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.opened {
		b.log.Info("provider circuit breaker closed: a model call succeeded")
	}
	b.failures, b.opened, b.probing = 0, false, false
}

// Failure records a provider failure. At the threshold, or on any failure while half-open, it opens
// and logs one line.
func (b *Breaker) Failure(reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	halfOpen := b.stateLocked() == HalfOpen
	b.probing = false
	b.failures++
	b.reason = reason
	if halfOpen || (!b.opened && b.failures >= b.threshold) {
		b.opened, b.openedAt = true, b.clk.Now()
		b.trips++
		b.log.Error(fmt.Sprintf("provider circuit breaker OPEN: all model calls paused for %s after %d consecutive provider failures; "+
			"reset with `ghostctl breaker reset` once the provider is fixed", b.cooldown, b.failures),
			"last_reason", reason, "trips", b.trips)
	}
}

// Reset is the operator action: close the breaker and clear the streak.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	was := b.opened
	b.failures, b.opened, b.probing = 0, false, false
	b.log.Warn("provider circuit breaker reset by operator", "was_open", was)
}

// Status snapshots the breaker.
func (b *Breaker) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := Status{State: b.stateLocked(), ConsecutiveFailures: b.failures, Threshold: b.threshold,
		CooldownSeconds: b.cooldown.Seconds(), TripsTotal: b.trips, RejectedTotal: b.rejected, LastReason: b.reason}
	if b.opened {
		at := b.openedAt
		s.OpenedAt = &at
	}
	return s
}
