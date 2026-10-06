package evalreport

// supervision.go — the human-supervision loaders (deltas, explanations, candidates, decisions)
// plus the small tag/spec helpers they and the other loaders share.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
)

func (w *world) loadDeltas(ctx context.Context, db claimstore.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT hd.id::text, hd.decision_episode_id::text,
 hd.literal_changes::text, COALESCE(to_jsonb(hd.semantic_labels)::text, '[]'), hd.unexplained,
 COALESCE(hd.candidate_criterion ->> 'suggested_eval_type', ''), hd.created_at,
 COALESCE((SELECT ev.evaluator::text || ':v' || ev.version FROM evaluator_versions ev
   WHERE ev.source_human_delta_id = hd.id ORDER BY ev.version LIMIT 1), '')
FROM human_deltas hd ORDER BY hd.created_at, hd.id`)
	if err != nil {
		return fmt.Errorf("evalreport: load deltas: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d deltaRow
		var changes, labels []byte
		if err := rows.Scan(&d.ID, &d.EpisodeID, &changes, &labels, &d.Unexplained,
			&d.Suggested, &d.CreatedAt, &d.SeededTag); err != nil {
			return fmt.Errorf("evalreport: scan delta: %w", err)
		}
		if err := json.Unmarshal(changes, &d.Changes); err != nil {
			return fmt.Errorf("evalreport: decode literal changes of delta %s: %w", d.ID, err)
		}
		if err := json.Unmarshal(labels, &d.Labels); err != nil {
			return fmt.Errorf("evalreport: decode semantic labels of delta %s: %w", d.ID, err)
		}
		d.CreatedAt = d.CreatedAt.UTC()
		w.deltas[d.ID] = &d
	}
	return rows.Err()
}

func (w *world) loadExplains(ctx context.Context, db claimstore.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT x.human_delta_id::text, x.eval_run_id::text,
 er.evaluator::text, er.evaluator_version, er.verdict
FROM human_delta_explanations x JOIN eval_runs er ON er.id = x.eval_run_id ORDER BY x.human_delta_id`)
	if err != nil {
		return fmt.Errorf("evalreport: load explanations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var x explRow
		var deltaID string
		if err := rows.Scan(&deltaID, &x.EvalRunID, &x.Evaluator, &x.Tag, &x.Verdict); err != nil {
			return fmt.Errorf("evalreport: scan explanation: %w", err)
		}
		w.explains[deltaID] = append(w.explains[deltaID], x)
	}
	return rows.Err()
}

func (w *world) loadCandidates(ctx context.Context, db claimstore.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT agent_run_id::text, draft_index, COALESCE(action_type, ''),
 to_recipients::text, cc_recipients::text, full_action_artifact::text FROM strategy_candidates`)
	if err != nil {
		return fmt.Errorf("evalreport: load candidates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var runID string
		var idx int
		var d draftJSON
		if err := rows.Scan(&runID, &idx, &d.ActionType, &d.To, &d.CC, &d.Artifact); err != nil {
			return fmt.Errorf("evalreport: scan candidate: %w", err)
		}
		if w.candidates[runID] == nil {
			w.candidates[runID] = map[int]draftJSON{}
		}
		w.candidates[runID][idx] = d
	}
	return rows.Err()
}

func (w *world) loadDecisions(ctx context.Context, db claimstore.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT h.decision_episode_id::text, h.send_decision, h.send_decided_at,
 COALESCE(c.draft_index, -1), COALESCE(c.action_type, ''),
 h.final_to::text, h.final_cc::text, h.final_artifact::text
FROM human_strategy_decisions h LEFT JOIN strategy_candidates c ON c.id = h.selected_candidate_id`)
	if err != nil {
		return fmt.Errorf("evalreport: load decisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h hsdRow
		var decided sql.NullTime
		if err := rows.Scan(&h.EpisodeID, &h.SendDecision, &decided, &h.SelectedDraft,
			&h.Final.ActionType, &h.Final.To, &h.Final.CC, &h.Final.Artifact); err != nil {
			return fmt.Errorf("evalreport: scan decision: %w", err)
		}
		if decided.Valid {
			t := decided.Time.UTC()
			h.SendDecidedAt = &t
		}
		w.decisions[h.EpisodeID] = &h
	}
	return rows.Err()
}

// loadRetirements reads the audited retirement time of each version (eval_promotions): the end of its
// in-force period when no successor's promotion supersedes it first.
func (w *world) loadRetirements(ctx context.Context, db claimstore.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT evaluator::text, version, MIN(created_at)
FROM eval_promotions WHERE to_status = 'retired' GROUP BY evaluator, version`)
	if err != nil {
		return fmt.Errorf("evalreport: load retirements: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var evaluator string
		var version int
		var at time.Time
		if err := rows.Scan(&evaluator, &version, &at); err != nil {
			return fmt.Errorf("evalreport: scan retirement: %w", err)
		}
		w.retired[keyOf(evaluator, version).tag] = at.UTC()
	}
	return rows.Err()
}

// orderKeys builds the sorted enumeration: every registered version plus every observed tag.
func (w *world) orderKeys() {
	for _, v := range w.versions {
		w.ordered = append(w.ordered, keyOf(v.Evaluator, v.Version))
	}
	sortKeys(w.ordered)
}

// parseTag reads '<evaluator>:v<N>'.
func parseTag(tag string) (versionKey, bool) {
	i := strings.LastIndex(tag, ":v")
	if i <= 0 {
		return versionKey{}, false
	}
	n, err := strconv.Atoi(tag[i+2:])
	if err != nil || n < 1 {
		return versionKey{}, false
	}
	return versionKey{evaluator: tag[:i], version: n, tag: tag}, true
}

// parseSpec decodes a shadow_spec payload. An invalid spec returns the empty spec plus the error; the
// loader records the error on the version (versionRow.SpecErr) instead of failing the report — a
// malformed spec must not break a metrics report, the affected metrics report n/a with the reason.
func parseSpec(raw string) (learning.Spec, error) {
	if raw == "" || raw == "null" {
		return learning.Spec{}, nil
	}
	var s learning.Spec
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return learning.Spec{}, fmt.Errorf("%w: %v", learning.ErrInvalidSpec, err)
	}
	return s, nil
}

// evalsByTag groups the version's final-draft eval rows by decided episode (byEpisode) and returns
// every row of the tag (rest), which the repeat-consistency and activation-window metrics read.
func (w *world) evalsByTag(tag string) (byEpisode map[string][]*evalRow, rest []*evalRow) {
	byEpisode = map[string][]*evalRow{}
	for _, r := range w.finalEvals {
		if r.Tag != tag {
			continue
		}
		ep := w.byRun[r.RunID]
		if ep == nil {
			continue
		}
		byEpisode[ep.ID] = append(byEpisode[ep.ID], r)
	}
	for _, r := range w.allEvals {
		if r.Tag == tag {
			rest = append(rest, r)
		}
	}
	return byEpisode, rest
}
