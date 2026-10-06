package bucket2

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// refTables maps an evidence ref kind to the table whose id it names. A ref whose id is a uuid must exist there.
var refTables = map[string]string{
	"candidate": "strategy_candidates", "activity": "activities", "knowledge": "knowledge",
	"human_strategy_decision": "human_strategy_decisions", "judgment_inference": "judgment_inferences",
	"eval": "eval_runs", "person": "people", "agent_run": "agent_runs", "decision_episode": "decision_episodes",
	"strategy_set": "strategy_sets", "dependency_invalidation": "agent_runs", "account_state": "accounts",
	"eval_result": "eval_runs", "customer_reaction": "customer_reactions", "business_outcome": "business_outcomes",
	"selection": "human_strategy_decisions", "semantic_edit": "judgment_inferences", "explicit_explanation": "judgment_verdicts", "gate_result": "gate_results",
}

// structural kinds name a derived part of a stored object by a field name or a key, not a row id. They are
// accepted only with a non-empty reference that is not a placeholder.
var editFields = map[string]bool{"recipients": true, "subject": true, "body": true, "channel": true, "attachments": true}

// derivedKinds name a derived part of a stored object by its row id: the id must exist in one of these tables.
var derivedKinds = map[string][]string{
	"ranking_rationale": {"strategy_sets", "strategy_candidates", "decision_rankings"}, "knowledge_use_claim": {"knowledge"},
	"final_artifact": {"strategy_candidates", "human_strategy_decisions"},
}

var placeholders = map[string]bool{"": true, "unknown": true, "none": true, "null": true, "n/a": true, "todo": true, "placeholder": true, "tbd": true}

// Querier is the read side shared by *sql.DB and *sql.Tx, so refs can be resolved inside a transaction.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ResolveRefs keeps only the evidence refs that point at something real (rule R1): an id of a known kind must exist
// in its table, and a structural ref must name something. An unresolvable or placeholder ref is dropped, so a result
// that rested only on such refs becomes unknown. The order of the surviving refs is kept.
func ResolveRefs(ctx context.Context, db Querier, refs []string) ([]string, error) {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ok, err := resolves(ctx, db, ref)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, ref)
		}
	}
	return out, nil
}

func resolves(ctx context.Context, db Querier, ref string) (bool, error) {
	kind, id, found := strings.Cut(ref, ":")
	if !found || placeholders[strings.ToLower(strings.TrimSpace(id))] {
		return false, nil
	}
	switch kind {
	case "artifact_field":
		return editFields[id], nil // only a field a human edit can touch
	case "state":
		// An AccountState field path counts only when a stored candidate cited it (core wrote it from the state it read).
		var n int
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM strategy_candidates WHERE $1 = ANY(state_refs)`, id).Scan(&n)
		return n > 0, wrap(ref, err)
	}
	if tables, ok := derivedKinds[kind]; ok {
		if !uuidPattern.MatchString(id) {
			return false, nil
		}
		for _, t := range tables {
			var exists bool
			if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1::uuid)`, t), id).Scan(&exists); err != nil {
				return false, wrap(ref, err)
			}
			if exists {
				return true, nil
			}
		}
		return false, nil
	}
	if kind == "effect" {
		var n int
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM agent_run_steps WHERE step = 'execute' AND detail->'recorded_effect'->>'idempotency_key' = $1`, id).Scan(&n)
		return n > 0, wrap(ref, err)
	}
	table, known := refTables[kind]
	if !known || !uuidPattern.MatchString(id) {
		return false, nil
	}
	var exists bool
	err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1::uuid)`, table), id).Scan(&exists)
	return exists, wrap(ref, err)
}

func wrap(ref string, err error) error {
	if err != nil {
		return fmt.Errorf("bucket2: resolve %s: %w", ref, err)
	}
	return nil
}
