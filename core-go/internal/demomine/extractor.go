package demomine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// ExtractorStats says how the LLM-extraction claims were served. The replay worker
// (bench/data/crmarena_replay_worker.py) answers only from recorded cassettes; it has no provider and no key.
type ExtractorStats struct {
	Calls           int `json:"calls"`
	ExactHits       int `json:"exact_hits"`
	ApproximateHits int `json:"approximate_hits"`
	// Misses are extractions the worker had no cassette for; they yield no claims (never a live call).
	Misses int `json:"misses"`
	// KnownMisses are the extractions the worker answered with no claims because the prompt is on its known-miss allowlist
	// (the prompts the mining also answered with no claims). They are not Misses: the frozen history is the reviewed one.
	KnownMisses int `json:"known_misses"`
	// KnownMissAllowlist is how many prompts the worker allows (0: none, every miss is unknown).
	KnownMissAllowlist int `json:"known_miss_allowlist"`
	// ApproximateEvents are the activities served by the worker's approximate tier (same email text, the
	// known-people block differs from the recording), in replay order. Their claims were recorded under a
	// different set of known people, so they are the ones to inspect for leakage.
	ApproximateEvents []ApproximateEvent `json:"approximate_events"`
}

// ApproximateEvent identifies one activity served by an approximate cassette hit. It holds no database id.
type ApproximateEvent struct {
	SourceSystem   string `json:"source_system"`
	SourceObjectID string `json:"source_object_id"`
	ActivityType   string `json:"activity_type"`
	OccurredAt     string `json:"occurred_at"`
}

// ReplayExtractor wraps the replay worker's client. A cassette miss is counted and answered with no claims, so
// one missing recording does not stop a replay; a worker that cannot be reached at all is an error.
type ReplayExtractor struct {
	inner claims.Extractor
	url   string
	// failOnMiss makes a miss the worker did not allowlist an error at once (the freeze), instead of an empty answer
	// that RequireNoMisses refuses later (the mining, which tolerates a gap).
	failOnMiss bool

	callMu sync.Mutex // serializes extraction so the worker's approximate counter can be attributed to one activity

	mu     sync.Mutex
	calls  int
	miss   int
	approx []ApproximateEvent
}

// ReplayOption configures a ReplayExtractor.
type ReplayOption func(*ReplayExtractor)

// FailOnMiss makes a cassette miss that is not on the worker's known-miss allowlist a hard error of the extraction
// (and so of the recompute that asked for it) rather than a counted empty answer.
func FailOnMiss() ReplayOption { return func(r *ReplayExtractor) { r.failOnMiss = true } }

// NewReplayExtractor returns an extractor on the replay worker at workerURL.
func NewReplayExtractor(workerURL string, opts ...ReplayOption) (*ReplayExtractor, error) {
	c, err := workerclient.New(workerURL, workerclient.WithTimeout(2*time.Minute))
	if err != nil {
		return nil, err
	}
	r := &ReplayExtractor{inner: c, url: strings.TrimRight(workerURL, "/")}
	for _, o := range opts {
		o(r)
	}
	return r, nil
}

// Extract implements claims.Extractor.
func (r *ReplayExtractor) Extract(ctx context.Context, req claims.ExtractRequest) (claims.ExtractResponse, error) {
	r.callMu.Lock()
	defer r.callMu.Unlock()
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	before, err := r.workerStats(ctx)
	if err != nil {
		return claims.ExtractResponse{}, err
	}
	resp, err := r.inner.Extract(ctx, req)
	if after, serr := r.workerStats(ctx); serr != nil {
		return claims.ExtractResponse{}, serr
	} else if after.Approximate > before.Approximate {
		r.mu.Lock()
		r.approx = append(r.approx, ApproximateEvent{SourceSystem: req.Activity.SourceSystem, SourceObjectID: req.Activity.SourceObjectID,
			ActivityType: req.Activity.Type, OccurredAt: req.Activity.OccurredAt.UTC().Format(time.RFC3339)})
		r.mu.Unlock()
	}
	if err == nil {
		return resp, nil
	}
	var werr *workerclient.Error
	// Only the worker's "no cassette" answer is a miss. Any other failure (a 5xx, a timeout, a bad request) is an error: turning
	// it into an empty answer would silently change the history.
	if errors.As(err, &werr) && werr.CassetteNotFound() {
		r.mu.Lock()
		r.miss++
		r.mu.Unlock()
		if r.failOnMiss {
			return claims.ExtractResponse{}, fmt.Errorf("demomine: no recorded cassette for activity %s and its prompt is not on the known-miss allowlist: %w", req.Activity.SourceObjectID, err)
		}
		return claims.ExtractResponse{Claims: []claims.Candidate{}, Model: "replay-cassette-miss", ExtractorVersion: claims.DefaultExtractorVersion}, nil
	}
	return claims.ExtractResponse{}, err
}

type workerCounters struct {
	Exact       int `json:"exact_hits"`
	Approximate int `json:"approximate_hits"`
	KnownMisses int `json:"known_miss_hits"`
	Allowlist   int `json:"known_misses"`
}

// workerStats reads the replay worker's hit counters.
func (r *ReplayExtractor) workerStats(ctx context.Context) (workerCounters, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url+"/replay-stats", nil)
	if err != nil {
		return workerCounters{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return workerCounters{}, fmt.Errorf("demomine: read replay stats: %w", err)
	}
	defer resp.Body.Close()
	var w workerCounters
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&w) != nil {
		return workerCounters{}, errors.New("demomine: the worker is not the replay worker (no /replay-stats)")
	}
	return w, nil
}

// RequireNoMisses fails when any extraction of this run found no cassette. A frozen history that contains an empty answer
// where the demo's recording has a real one is not the history the demo was reviewed on, so a freeze with a miss must not be
// sealed. A nil extractor (a freeze without replay) has nothing to check.
func (r *ReplayExtractor) RequireNoMisses() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.miss > 0 {
		return fmt.Errorf("demomine: %d of %d extractions had no recorded cassette; the history would differ from the reviewed one, so it is not sealed (re-run the setup; if it persists the recordings are incomplete)", r.miss, r.calls)
	}
	return nil
}

// Stats reads the worker's hit counters and adds this client's call and miss counts and the approximate events.
func (r *ReplayExtractor) Stats(ctx context.Context) (ExtractorStats, error) {
	w, err := r.workerStats(ctx)
	if err != nil {
		return ExtractorStats{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	events := append([]ApproximateEvent{}, r.approx...)
	return ExtractorStats{Calls: r.calls, ExactHits: w.Exact, ApproximateHits: w.Approximate, Misses: r.miss, KnownMisses: w.KnownMisses, KnownMissAllowlist: w.Allowlist, ApproximateEvents: events}, nil
}
