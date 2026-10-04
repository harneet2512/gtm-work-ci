package knowledgestore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// ListApplicableAsOf loads the knowledge a decision at asOf could have used and nothing learned later (HAR-117
// invariant I3, no future knowledge leaks into guidance). A knowledge object exists at asOf when it was created
// by then; its status at asOf is not the stored status (that reflects evidence that may postdate asOf) but the
// status its lifecycle earned from the evidence recorded up to asOf, replayed in order through the same pure
// rules (ADR-0013). The returned objects carry the counts, supporting episodes and counterexamples of that
// moment. Evidence is ordered by its own time (knowledge_evidence.created_at), never by when it was inserted.
func ListApplicableAsOf(ctx context.Context, q Querier, rules knowledge.Rules, asOf time.Time) ([]knowledge.Knowledge, error) {
	if asOf.IsZero() {
		return nil, fmt.Errorf("list applicable knowledge as of: a time is required")
	}
	rows, err := q.QueryContext(ctx, `SELECT id FROM knowledge WHERE created_at <= $1 ORDER BY key`, asOf.UTC())
	if err != nil {
		return nil, fmt.Errorf("list knowledge as of %s: %w", asOf.UTC().Format(time.RFC3339), err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	out := make([]knowledge.Knowledge, 0, len(ids))
	for _, id := range ids {
		k, err := Get(ctx, q, id)
		if err != nil {
			return nil, err
		}
		then, err := replayTo(ctx, q, k, rules, asOf)
		if err != nil {
			return nil, err
		}
		if rules.Applicable(then.Status) {
			out = append(out, then)
		}
	}
	return out, nil
}

// replayTo folds the evidence recorded up to asOf into k's starting point (an unsupported candidate).
func replayTo(ctx context.Context, q Querier, stored knowledge.Knowledge, rules knowledge.Rules, asOf time.Time) (knowledge.Knowledge, error) {
	k := stored
	k.Status, k.Counts, k.LastValidatedAt = knowledge.StatusCandidate, knowledge.Counts{}, nil
	k.SupportingDecisionEpisodeIDs, k.Counterexamples, k.StatusHistory = []string{}, []knowledge.Counterexample{}, nil
	rows, err := q.QueryContext(ctx, `SELECT kind, ref_id::text, coalesce(note, ''), created_at FROM knowledge_evidence
		WHERE knowledge_id = $1 AND created_at <= $2 ORDER BY created_at, id`, stored.ID, asOf.UTC())
	if err != nil {
		return knowledge.Knowledge{}, fmt.Errorf("load evidence of %s as of %s: %w", stored.ID, asOf.UTC().Format(time.RFC3339), err)
	}
	var evidence []knowledge.Evidence
	for rows.Next() {
		var ev knowledge.Evidence
		var note string
		if err := rows.Scan(&ev.Kind, &ev.RefID, &note, &ev.At); err != nil {
			rows.Close()
			return knowledge.Knowledge{}, err
		}
		ev.At = ev.At.UTC()
		ev.Polarity, ev.OutcomeType, ev.Note = strings.TrimPrefix(note, "polarity="), strings.TrimPrefix(note, "outcome="), note
		evidence = append(evidence, ev)
	}
	if err := closeRows(rows); err != nil {
		return knowledge.Knowledge{}, err
	}
	for _, ev := range evidence {
		if k, _, err = knowledge.Record(k, ev, rules); err != nil {
			return knowledge.Knowledge{}, fmt.Errorf("replay evidence of %s: %w", stored.ID, err)
		}
	}
	k, _ = knowledge.EvaluateLifecycle(k, asOf, rules)
	return k, nil
}
