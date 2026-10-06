package strategystore_test

import "testing"

// A semantic eval that warned on the dimension the human then edited explains the edit (HAR-97 D6). Before,
// only a failing DETERMINISTIC eval that now passes counted, so a CTA edit predicted by a semantic WARN was
// always reported unexplained and seeded candidate knowledge.
func TestSemanticWarnOnTheEditedDimensionExplainsTheDelta(t *testing.T) {
	f := newFixture(t)
	var draftIndex int
	if err := env.DB.QueryRow(`SELECT draft_index FROM strategy_candidates WHERE id = $1::uuid`, f.seed.Candidates[1]).Scan(&draftIndex); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO eval_runs (agent_run_id, evaluator, evaluator_version, kind, verdict, rationale, draft_index, evidence_class)
 VALUES ($1::uuid, 'cta_calibration', 'cta_calibration:v1', 'semantic', 'warn', 'the ask is stronger than the buyer timing', $2, 'methodology')`,
		f.seed.RunID, draftIndex); err != nil {
		t.Fatal(err)
	}
	editAndSend(t, f)
	m := decode(t, deltaDoc(t, f.seed.EpisodeID))
	if m["unexplained"] != false {
		t.Fatalf("a semantic warn on cta must explain the cta/body edit, got unexplained=%v", m["unexplained"])
	}
	if ids, _ := m["explained_by_eval_result_ids"].([]any); len(ids) != 1 {
		t.Fatalf("explained_by = %v, want the semantic eval", ids)
	}
}

func TestSemanticWarnOnAnotherDimensionDoesNotExplainTheDelta(t *testing.T) {
	f := newFixture(t)
	var draftIndex int
	if err := env.DB.QueryRow(`SELECT draft_index FROM strategy_candidates WHERE id = $1::uuid`, f.seed.Candidates[1]).Scan(&draftIndex); err != nil {
		t.Fatal(err)
	}
	// state_transition_support is a state dimension; the edits express cta, factual, timing, strategy or style, not state.
	if _, err := env.DB.Exec(`INSERT INTO eval_runs (agent_run_id, evaluator, evaluator_version, kind, verdict, rationale, draft_index, evidence_class)
 VALUES ($1::uuid, 'state_transition_support', 'state_transition_support:v1', 'semantic', 'warn', 'the transition is weakly supported', $2, 'methodology')`,
		f.seed.RunID, draftIndex); err != nil {
		t.Fatal(err)
	}
	editAndSend(t, f)
	m := decode(t, deltaDoc(t, f.seed.EpisodeID))
	if m["unexplained"] != true {
		t.Fatalf("a semantic warn on an untouched dimension must not explain the edit, got %v", m["unexplained"])
	}
}

func TestAnUnrelatedSemanticWarnDoesNotExplainAParagraphEdit(t *testing.T) {
	f := newFixture(t)
	var draftIndex int
	if err := env.DB.QueryRow(`SELECT draft_index FROM strategy_candidates WHERE id = $1::uuid`, f.seed.Candidates[1]).Scan(&draftIndex); err != nil {
		t.Fatal(err)
	}
	// strategy-class warn about something else entirely: the reason shares no substantive word with the edited text.
	if _, err := env.DB.Exec(`INSERT INTO eval_runs (agent_run_id, evaluator, evaluator_version, kind, verdict, rationale, draft_index, evidence_class)
 VALUES ($1::uuid, 'customer_risk_sensitivity', 'customer_risk_sensitivity:v1', 'semantic', 'warn', 'open support escalation raises churn exposure', $2, 'methodology')`,
		f.seed.RunID, draftIndex); err != nil {
		t.Fatal(err)
	}
	editAndSend(t, f)
	if m := decode(t, deltaDoc(t, f.seed.EpisodeID)); m["unexplained"] != true {
		t.Fatalf("an unrelated warn must not explain the paragraph edit, got unexplained=%v", m["unexplained"])
	}
}
