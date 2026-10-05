package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/evalarea"
)

// Span kinds (episode_trace.v1.json spanKind), in causal order.
const (
	kindSource       = "source_event"
	kindEvidence     = "evidence"
	kindResolution   = "resolution"
	kindGraph        = "graph_mutation"
	kindState        = "state"
	kindPrecedents   = "precedents"
	kindRetrieved    = "knowledge_retrieved"
	kindApplicable   = "knowledge_applicable"
	kindUsed         = "knowledge_used"
	kindCandidates   = "candidates"
	kindRanking      = "ranking"
	kindCliff        = "cliff_message"
	kindHuman        = "human_interaction"
	kindRecomputed   = "recomputed_action"
	kindKnowledgeMut = "knowledge_mutation"
)

// Span statuses (episode_trace.v1.json traceSpan.status).
const (
	statusRecorded    = "recorded"
	statusPending     = "pending"
	statusNotRecorded = "not_recorded"
)

// SpanRef points at a row behind a span.
type SpanRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// TraceSpan is episode_trace.v1.json traceSpan.
type TraceSpan struct {
	ID            string          `json:"id"`
	Seq           int             `json:"seq"`
	Kind          string          `json:"kind"`
	Title         string          `json:"title"`
	Status        string          `json:"status"`
	OccurredAt    *time.Time      `json:"occurred_at"`
	Summary       string          `json:"summary"`
	Refs          []SpanRef       `json:"refs"`
	EvidenceRefs  json.RawMessage `json:"evidence_refs,omitempty"`
	EvalResultIDs []string        `json:"eval_result_ids"`
	Attributes    map[string]any  `json:"attributes"`
}

// EpisodeTrace is episode_trace.v1.json.
type EpisodeTrace struct {
	EpisodeID               string      `json:"episode_id"`
	AgentRunID              string      `json:"agent_run_id"`
	AccountID               string      `json:"account_id"`
	Spans                   []TraceSpan `json:"spans"`
	UnassignedEvalResultIDs []string    `json:"unassigned_eval_result_ids"`
}

// span makes a span of kind with a stable id (kind:ref) and empty, non-nil lists.
func span(kind, ref, title, status, summary string) TraceSpan {
	return TraceSpan{ID: kind + ":" + ref, Kind: kind, Title: title, Status: status, Summary: summary,
		Refs: []SpanRef{}, EvalResultIDs: []string{}, Attributes: map[string]any{}}
}

func at(t time.Time) *time.Time {
	u := t.UTC()
	return &u
}

// Trace returns the causal trace of a DecisionEpisode: TraceSpans in order, each with its refs and the EvalResults
// that judged it. ErrNotFound: no such episode.
func (r *Reader) Trace(ctx context.Context, episodeID string) (EpisodeTrace, error) {
	var out EpisodeTrace
	err := r.snapshot(ctx, func(db claimstore.DB) error {
		in, err := loadTraceInput(ctx, db, episodeID)
		if err != nil {
			return err
		}
		out = assemble(in)
		return nil
	})
	return out, err
}

// assemble builds the spans in causal order, numbers them, and attaches every EvalResult of the run to the span of its
// family (the rest are listed as unassigned, so each result is reachable from the trace exactly once).
func assemble(in *traceInput) EpisodeTrace {
	spans := []TraceSpan{
		in.sourceSpan(), in.evidenceSpan(), in.resolutionSpan(), in.graphSpan(), in.stateSpan(), in.precedentsSpan(),
		in.knowledgeSpan(kindRetrieved), in.knowledgeSpan(kindApplicable), in.knowledgeSpan(kindUsed),
		in.candidatesSpan(), in.rankingSpan(),
	}
	spans = append(spans, in.cliffSpans()...)
	spans = append(spans, in.humanSpan(), in.recomputedSpan(), in.mutationSpan())

	byKind := map[string][]string{}
	unassigned := []string{}
	for _, r := range in.results {
		if r.SendTime {
			// The send-time re-evaluation judged the final artifact: it belongs to the recomputed action, not the candidates.
			byKind[kindRecomputed] = append(byKind[kindRecomputed], r.ID)
		} else if kind, ok := evalarea.SpanOfEvalType(r.EvalType); ok {
			byKind[kind] = append(byKind[kind], r.ID)
		} else {
			unassigned = append(unassigned, r.ID)
		}
	}
	for i := range spans {
		spans[i].Seq = i + 1
		spans[i].Summary = clipRunes(spans[i].Summary, maxSummaryRunes)
		if ids := byKind[spans[i].Kind]; len(ids) > 0 {
			spans[i].EvalResultIDs = slices.Clone(ids)
		}
	}
	return EpisodeTrace{EpisodeID: in.episode.ID, AgentRunID: in.episode.RunID, AccountID: in.episode.AccountID, Spans: spans, UnassignedEvalResultIDs: unassigned}
}

// maxSummaryRunes is episode_trace.v1.json traceSpan.summary maxLength.
const maxSummaryRunes = 600

// clipRunes cuts s to n characters (not bytes), so a long activity summary never breaks the contract bound.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func countWord(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
