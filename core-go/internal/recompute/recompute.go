package recompute

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalinput"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// ErrNotFound: the run does not exist or has no decision episode (404).
var ErrNotFound = errors.New("recompute: not found")

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Service answers GET /runs/{run_id}/recomputation.
type Service struct {
	db  *sql.DB
	clk clock.Clock
}

// New returns a Service over db. A nil clock selects the wall clock.
func New(db *sql.DB, clk clock.Clock) (*Service, error) {
	if db == nil {
		return nil, errors.New("recompute: a database is required")
	}
	if clk == nil {
		clk = clock.Real{}
	}
	return &Service{db: db, clk: clk}, nil
}

// Recomputation derives the invalidation of the run's chosen action from stored facts.
func (s *Service) Recomputation(ctx context.Context, runID string) (Invalidation, error) {
	if !uuidPattern.MatchString(runID) {
		return Invalidation{}, ErrNotFound
	}
	f, err := s.load(ctx, runID)
	if err != nil {
		return Invalidation{}, err
	}
	f.Now = s.clk.Now()
	return derive(f)
}

// load reads the facts of a run. Each read is one the decision path itself wrote: nothing is recomputed here.
func (s *Service) load(ctx context.Context, runID string) (facts, error) {
	f := facts{RunID: runID}
	var version sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT r.account_id::text, r.state_version, e.id::text, COALESCE(e.human_delta_id::text, '')
 FROM agent_runs r JOIN decision_episodes e ON e.agent_run_id = r.id WHERE r.id = $1::uuid ORDER BY e.created_at LIMIT 1`, runID).
		Scan(&f.AccountID, &version, &f.EpisodeID, &f.DeltaID)
	if errors.Is(err, sql.ErrNoRows) {
		return facts{}, ErrNotFound
	}
	if err != nil {
		return facts{}, fmt.Errorf("recompute: read run %s: %w", runID, err)
	}
	if version.Valid {
		v := int(version.Int64)
		f.StateBefore = &v
	}
	var saved []byte
	var selected string
	err = s.db.QueryRowContext(ctx, `SELECT id::text, selected_candidate_id::text, send_decision, edits::text FROM human_strategy_decisions
 WHERE decision_episode_id = $1::uuid`, f.EpisodeID).Scan(&f.DecisionID, &selected, &f.SendDecision, &saved)
	if errors.Is(err, sql.ErrNoRows) {
		return f, nil // nothing chosen yet: not_decided
	}
	if err != nil {
		return facts{}, fmt.Errorf("recompute: read the decision of episode %s: %w", f.EpisodeID, err)
	}
	f.Decided = true
	if f.Edits, f.Labels, err = s.edits(ctx, f.DeltaID, saved); err != nil {
		return facts{}, err
	}
	if err := s.candidate(ctx, selected, &f); err != nil {
		return facts{}, err
	}
	if f.SendDecision == "send" {
		if err := s.sendTime(ctx, &f); err != nil {
			return facts{}, err
		}
	}
	return f, nil
}

type literal struct {
	Kind   string          `json:"kind"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

// edits are the HumanDelta's literal changes when the send wrote one (and the worker's labels), else the edits
// saved with the decision.
func (s *Service) edits(ctx context.Context, deltaID string, saved []byte) ([]literalEdit, []string, error) {
	raw, labels := saved, []string{}
	if deltaID != "" {
		var literalJSON, labelsJSON string
		if err := s.db.QueryRowContext(ctx, `SELECT literal_changes::text, to_jsonb(semantic_labels)::text FROM human_deltas WHERE id = $1::uuid`, deltaID).
			Scan(&literalJSON, &labelsJSON); err != nil {
			return nil, nil, fmt.Errorf("recompute: read human delta %s: %w", deltaID, err)
		}
		raw = []byte(literalJSON)
		if err := json.Unmarshal([]byte(labelsJSON), &labels); err != nil {
			return nil, nil, fmt.Errorf("recompute: decode the labels of delta %s: %w", deltaID, err)
		}
	}
	var lits []literal
	if err := json.Unmarshal(raw, &lits); err != nil {
		return nil, nil, fmt.Errorf("recompute: decode the edits: %w", err)
	}
	out := make([]literalEdit, 0, len(lits))
	for _, l := range lits {
		e := literalEdit{Kind: l.Kind}
		for _, p := range []struct {
			raw json.RawMessage
			dst *any
		}{{l.Before, &e.Before}, {l.After, &e.After}} {
			if len(p.raw) > 0 {
				if err := json.Unmarshal(p.raw, p.dst); err != nil {
					return nil, nil, fmt.Errorf("recompute: decode an edit's value: %w", err)
				}
			}
		}
		out = append(out, e)
	}
	return out, labels, nil
}

// candidate reads the chosen candidate and its bundle: the evals on the OLD artifact.
func (s *Service) candidate(ctx context.Context, id string, f *facts) error {
	var knowledge, bundle string
	if err := s.db.QueryRowContext(ctx, `SELECT c.id::text, c.ranking, to_jsonb(c.knowledge_refs)::text, c.eval_bundle_id::text
 FROM strategy_candidates c WHERE c.id = $1::uuid`, id).Scan(&f.CandidateID, &f.Ranking, &knowledge, &bundle); err != nil {
		return fmt.Errorf("recompute: read candidate %s: %w", id, err)
	}
	if err := json.Unmarshal([]byte(knowledge), &f.KnowledgeIDs); err != nil {
		return fmt.Errorf("recompute: decode the knowledge refs of candidate %s: %w", id, err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT it -> 'result' ->> 'id', it -> 'result' ->> 'eval_type', it -> 'result' ->> 'kind', it -> 'result' ->> 'verdict'
 FROM eval_bundles b, jsonb_array_elements(b.items) WITH ORDINALITY AS t(it, n)
 WHERE b.id = $1::uuid AND jsonb_typeof(it -> 'result') = 'object' ORDER BY n`, bundle)
	if err != nil {
		return fmt.Errorf("recompute: read the bundle of candidate %s: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var j judged
		if err := rows.Scan(&j.ID, &j.EvalType, &j.Kind, &j.Verdict); err != nil {
			return fmt.Errorf("recompute: scan a bundle result: %w", err)
		}
		f.Old = append(f.Old, j)
	}
	return rows.Err()
}

// sendTime reads what the send re-evaluated: the linked batch, and the state the evaluation stored it read (its
// version and digest). Nothing is re-derived here: a state recomputed at read time would be compared with itself.
// The state the run read is looked up by its pinned version and digested the same way.
func (s *Service) sendTime(ctx context.Context, f *facts) error {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id::text, r.evaluator::text, r.kind, r.verdict, l.state_version, l.state_hash FROM send_eval_results l
 JOIN eval_runs r ON r.id = l.eval_run_id WHERE l.human_strategy_decision_id = $1::uuid ORDER BY r.evaluator::text, r.id`, f.DecisionID)
	if err != nil {
		return fmt.Errorf("recompute: read the send-time batch of decision %s: %w", f.DecisionID, err)
	}
	defer rows.Close()
	versions := map[int]bool{}
	hashes := map[string]bool{}
	for rows.Next() {
		var j judged
		var version int
		var hash string
		if err := rows.Scan(&j.ID, &j.EvalType, &j.Kind, &j.Verdict, &version, &hash); err != nil {
			return fmt.Errorf("recompute: scan a send-time result: %w", err)
		}
		f.New = append(f.New, j)
		versions[version], hashes[hash] = true, true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	f.Linked = len(f.New) > 0
	// one send, one state: a batch that names two states proves nothing, so it is left unknown
	if len(versions) == 1 && len(hashes) == 1 {
		for v := range versions {
			f.StateAfter = &v
		}
		for h := range hashes {
			f.HashAfter = h
		}
	}
	return s.hashBefore(ctx, f)
}

// hashBefore digests the state version the run was pinned to (agent_runs.state_version), read from the state
// history. A version with no stored document leaves the digest unknown.
func (s *Service) hashBefore(ctx context.Context, f *facts) error {
	if f.StateBefore == nil {
		return nil
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT state FROM state_history WHERE account_id = $1::uuid AND version = $2`, f.AccountID, *f.StateBefore).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("recompute: read the state v%d of account %s: %w", *f.StateBefore, f.AccountID, err)
	}
	var st reducer.AccountState
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("recompute: decode the state v%d of account %s: %w", *f.StateBefore, f.AccountID, err)
	}
	if f.HashBefore, err = evalinput.StateDigest(st); err != nil {
		return err
	}
	return nil
}
