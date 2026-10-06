package providerbreaker

import (
	"context"
	"errors"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// ErrOpen is returned (wrapped in an OpenError) for a call refused because the breaker is open.
var ErrOpen = errors.New("providerbreaker: provider circuit breaker is open")

// OpenError is a call refused locally. It is a provider-unavailable error: the job is parked, not retried.
type OpenError struct{}

func (OpenError) Error() string { return ErrOpen.Error() + "; model calls are paused" }

// Is makes errors.Is(err, ErrOpen) work.
func (OpenError) Is(target error) bool { return target == ErrOpen }

// ProviderUnavailable marks the error as non-retryable provider trouble (claims.IsProviderUnavailable).
func (OpenError) ProviderUnavailable() bool { return true }

// Guard is a claims.Extractor that consults the breaker before every call and feeds it the outcome.
type Guard struct {
	Inner   claims.Extractor
	Breaker *Breaker
}

// Extract implements claims.Extractor.
func (g Guard) Extract(ctx context.Context, req claims.ExtractRequest) (claims.ExtractResponse, error) {
	if !g.Breaker.Allow() {
		return claims.ExtractResponse{}, OpenError{}
	}
	resp, err := g.Inner.Extract(ctx, req)
	switch {
	case err == nil:
		g.Breaker.Success()
	case claims.IsProviderFault(err):
		g.Breaker.Failure(err.Error())
	default:
		g.Breaker.ReleaseProbe() // a request error says nothing about the provider
	}
	return resp, err
}
