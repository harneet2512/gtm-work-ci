package claims

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// DefaultExtractorVersion is the worker prompt version requested when none is configured.
const DefaultExtractorVersion = "extract-v4"

// MaxExtractTextRunes is the worker's text limit (worker.yaml /v1/extract maxLength).
const MaxExtractTextRunes = 60000

// ExtractRequest is one /v1/extract call.
type ExtractRequest struct {
	Activity         ActivityInput
	Text             string
	KnownPeople      []KnownPerson
	ExtractorVersion string
}

// ExtractResponse is the worker's answer: candidates whose quotes it already verified, and how
// many it rejected.
type ExtractResponse struct {
	Claims           []Candidate `json:"claims"`
	Model            string      `json:"model"`
	ExtractorVersion string      `json:"extractor_version"`
	Dropped          int         `json:"dropped"`
}

// Extractor turns free text into claim candidates. The production implementation is the HTTP
// client of the Python worker (internal/workerclient); tests use fakes. No LLM is ever called
// from core directly.
type Extractor interface {
	Extract(ctx context.Context, req ExtractRequest) (ExtractResponse, error)
}

// Cache stores worker answers per (activity, extractor version) so recomputes and replays never
// pay for the same extraction twice.
type Cache interface {
	Get(ctx context.Context, activityID, extractorVersion string) (ExtractResponse, bool, error)
	Put(ctx context.Context, activityID, extractorVersion string, resp ExtractResponse) error
}

// extractableTypes are the activity types whose body text may carry claims.
var extractableTypes = map[string]bool{
	"EmailReceived": true, "EmailReply": true, "EmailSent": true, "TranscriptReady": true,
	"SlackMessage": true, "SlackDecision": true, "CRMNoteAdded": true,
}

// Extractable reports whether an activity's text should go to the model worker.
func Extractable(act ActivityInput) bool {
	return extractableTypes[act.Type] && strings.TrimSpace(act.Body) != ""
}

// Pipeline produces every claim an activity implies: deterministic rules for structured
// changes, the model worker (through the cache) for free text.
type Pipeline struct {
	Rules   RuleExtractor
	LLM     Extractor // nil disables model extraction
	Cache   Cache     // nil disables caching
	Version string    // extractor version; DefaultExtractorVersion when empty
}

// Output is what the pipeline produced for one activity.
type Output struct {
	Claims   []Claim
	Skipped  []Skip
	Dropped  []Drop
	LLMCalls int // worker calls made (0 on a cache hit)
}

// Run extracts claims for act. known and resolve describe the people of the account. When only the
// model call fails, the returned Output still holds the deterministic rule claims next to the error.
func (p Pipeline) Run(ctx context.Context, act ActivityInput, known []KnownPerson, resolve Resolver) (Output, error) {
	rules, err := p.Rules.Extract(ctx, act)
	if err != nil {
		return Output{}, err
	}
	out := Output{Claims: rules.Claims, Skipped: rules.Skipped}
	if p.LLM == nil || !Extractable(act) {
		return out, nil
	}
	version := p.Version
	if version == "" {
		version = DefaultExtractorVersion
	}
	resp, calls, err := p.extract(ctx, act, known, version)
	out.LLMCalls = calls
	if err != nil {
		return out, err // the deterministic claims still stand; only the model's are missing
	}
	conv := FromCandidates(act, resp.Claims, resp.Model, resp.ExtractorVersion, resolve)
	out.Claims = append(out.Claims, conv.Claims...)
	out.Dropped = conv.Dropped
	return out, nil
}

func (p Pipeline) extract(ctx context.Context, act ActivityInput, known []KnownPerson, version string) (ExtractResponse, int, error) {
	if p.Cache != nil {
		cached, ok, err := p.Cache.Get(ctx, act.ID, version)
		if err != nil {
			return ExtractResponse{}, 0, fmt.Errorf("claims: read extraction cache for %s: %w", act.ID, err)
		}
		if ok {
			return cached, 0, nil
		}
	}
	text := truncateText(act.Body)
	resp, err := p.LLM.Extract(ctx, ExtractRequest{Activity: act, Text: text, KnownPeople: known, ExtractorVersion: version})
	if err != nil {
		return ExtractResponse{}, 1, fmt.Errorf("claims: extract activity %s: %w", act.ID, err)
	}
	if resp.Model == "" {
		return ExtractResponse{}, 1, Permanent(errors.New("claims: worker response names no model"))
	}
	if resp.ExtractorVersion == "" {
		resp.ExtractorVersion = version
	}
	if p.Cache != nil {
		if err := p.Cache.Put(ctx, act.ID, version, resp); err != nil {
			return ExtractResponse{}, 1, fmt.Errorf("claims: write extraction cache for %s: %w", act.ID, err)
		}
	}
	return resp, 1, nil
}

func truncateText(s string) string {
	if r := []rune(s); len(r) > MaxExtractTextRunes {
		return string(r[:MaxExtractTextRunes])
	}
	return s
}
