package providerbreaker

import (
	"context"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// WorkerCalls is the model-backed part of the worker API the run orchestrator uses (workerclient.Client
// implements it).
type WorkerCalls interface {
	Strategies(ctx context.Context, req workerclient.StrategiesRequest) (workerclient.StrategiesResponse, error)
	Judge(ctx context.Context, req workerclient.JudgeRequest) (workerclient.JudgeResponse, error)
	Revise(ctx context.Context, req workerclient.ReviseRequest) (workerclient.ReviseResponse, error)
}

// WorkerGuard puts the same breaker Guard uses for extraction in front of /v1/strategies, /v1/judge and
// /v1/revise: with the breaker open every call is refused locally (an OpenError, a provider-unavailable error),
// so a down provider costs zero paid calls instead of 3 + 3xN failing ones per run (HAR-135). An optional
// Limiter paces the calls to the provider's requests-per-minute ceiling (HAR-117), so a run does not spend its
// retry budget racing a limit it cannot see.
type WorkerGuard struct {
	Inner   WorkerCalls
	Breaker *Breaker
	Limiter *Limiter // nil: no pacing
}

// guarded runs call behind the breaker and the limiter, then feeds the breaker the outcome, exactly as
// Guard.Extract does. The breaker is consulted first, so an open breaker is refused at once and never waits
// on the limiter.
func guarded[T any](ctx context.Context, b *Breaker, limiter *Limiter, call func() (T, error)) (T, error) {
	var zero T
	if !b.Allow() {
		return zero, OpenError{}
	}
	if limiter != nil {
		if err := limiter.Wait(ctx); err != nil {
			b.ReleaseProbe() // no call was made, so there is no verdict on the provider
			return zero, err
		}
	}
	out, err := call()
	switch {
	case err == nil:
		b.Success()
	case claims.IsProviderFault(err):
		b.Failure(err.Error())
	default:
		b.ReleaseProbe() // a request error says nothing about the provider
	}
	return out, err
}

// Strategies implements WorkerCalls.
func (g WorkerGuard) Strategies(ctx context.Context, req workerclient.StrategiesRequest) (workerclient.StrategiesResponse, error) {
	return guarded(ctx, g.Breaker, g.Limiter, func() (workerclient.StrategiesResponse, error) { return g.Inner.Strategies(ctx, req) })
}

// Judge implements WorkerCalls.
func (g WorkerGuard) Judge(ctx context.Context, req workerclient.JudgeRequest) (workerclient.JudgeResponse, error) {
	return guarded(ctx, g.Breaker, g.Limiter, func() (workerclient.JudgeResponse, error) { return g.Inner.Judge(ctx, req) })
}

// Revise implements WorkerCalls.
func (g WorkerGuard) Revise(ctx context.Context, req workerclient.ReviseRequest) (workerclient.ReviseResponse, error) {
	return guarded(ctx, g.Breaker, g.Limiter, func() (workerclient.ReviseResponse, error) { return g.Inner.Revise(ctx, req) })
}
