// Package claimstest holds test support for the claims pipeline: a scriptable fake of the model
// worker (no live LLM is ever called from tests) and the gold-checkpoint scoring helpers.
package claimstest

import (
	"context"
	"sync"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// FakeExtractor is a claims.Extractor whose answers are scripted. It is safe for concurrent use.
type FakeExtractor struct {
	// Model is reported in every response (default "fake-model").
	Model string
	// Candidates returns the candidates for a request; nil means no candidates.
	Candidates func(req claims.ExtractRequest) []claims.Candidate
	// Err, when set and returning non-nil, fails the call (for retry and parking tests).
	Err func(req claims.ExtractRequest) error

	mu    sync.Mutex
	calls []claims.ExtractRequest
}

// Extract implements claims.Extractor.
func (f *FakeExtractor) Extract(_ context.Context, req claims.ExtractRequest) (claims.ExtractResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	if f.Err != nil {
		if err := f.Err(req); err != nil {
			return claims.ExtractResponse{}, err
		}
	}
	model := f.Model
	if model == "" {
		model = "fake-model"
	}
	resp := claims.ExtractResponse{Model: model, ExtractorVersion: req.ExtractorVersion}
	if f.Candidates != nil {
		resp.Claims = f.Candidates(req)
	}
	return resp, nil
}

// Calls returns a copy of the requests received so far.
func (f *FakeExtractor) Calls() []claims.ExtractRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]claims.ExtractRequest(nil), f.calls...)
}

// CallsFor counts the requests made for one activity.
func (f *FakeExtractor) CallsFor(activityID string) int {
	n := 0
	for _, c := range f.Calls() {
		if c.Activity.ID == activityID {
			n++
		}
	}
	return n
}
