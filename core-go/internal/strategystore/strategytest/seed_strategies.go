package strategytest

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

type raw = map[string]json.RawMessage

func decodeObject(b []byte) (raw, error) {
	var m raw
	return m, json.Unmarshal(b, &m)
}

func seedStrategies(tx exec, s *Seeded, ex examples, version int, diffID string) error {
	set, err := decodeObject(ex.set)
	if err != nil {
		return err
	}
	var candidates []raw
	if err := json.Unmarshal(set["candidates"], &candidates); err != nil || len(candidates) != 3 {
		return fmt.Errorf("the strategy set example needs 3 candidates: %v", err)
	}
	for i := range candidates {
		if err := seedBundle(tx, s, ex.bundle, i); err != nil {
			return err
		}
	}
	stateRef := fmt.Sprintf(`{"account_id":%q,"version":%d}`, s.AccountID, version)
	if _, err := tx.Exec(`INSERT INTO strategy_sets (id, decision_episode_id, agent_run_id, account_id, generated_at, state_ref, state_diff_id, trigger_activity_ids)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::timestamptz, $6::jsonb, $7::uuid, ARRAY[$8::uuid])`,
		s.SetID, s.EpisodeID, s.RunID, s.AccountID, unquote(set["generated_at"]), stateRef, diffID, s.ActivityID); err != nil {
		return fmt.Errorf("strategy set: %w", err)
	}
	for i, c := range candidates {
		if err := seedCandidate(tx, s, c, i); err != nil {
			return err
		}
	}
	return nil
}

func seedBundle(tx exec, s *Seeded, bundleExample []byte, i int) error {
	b, err := decodeObject(bundleExample)
	if err != nil {
		return err
	}
	var items []raw
	if err := json.Unmarshal(b["items"], &items); err != nil {
		return err
	}
	for _, it := range items {
		if string(it["result"]) == "null" {
			continue
		}
		res, err := decodeObject(it["result"])
		if err != nil {
			return err
		}
		res["draft_index"] = json.RawMessage(fmt.Sprint(i + 1))
		if it["result"], err = json.Marshal(res); err != nil {
			return err
		}
	}
	enc, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO eval_bundles (id, agent_run_id, draft_index, items, generated_at) VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, $5::timestamptz)`,
		s.Bundles[i], s.RunID, i+1, string(enc), unquote(b["generated_at"]))
	if err != nil {
		return fmt.Errorf("eval bundle %d: %w", i+1, err)
	}
	return nil
}

func seedCandidate(tx exec, s *Seeded, c raw, i int) error {
	var rank int
	var preferred bool
	if err := json.Unmarshal(c["ranking"], &rank); err != nil {
		return err
	}
	if err := json.Unmarshal(c["preferred_by_agent"], &preferred); err != nil {
		return err
	}
	var stateRefs, knowledge []string
	if err := json.Unmarshal(c["state_refs"], &stateRefs); err != nil {
		return err
	}
	if err := json.Unmarshal(c["knowledge_refs"], &knowledge); err != nil {
		return err
	}
	var subject any
	if string(c["subject"]) != "null" {
		subject = unquote(c["subject"])
	}
	_, err := tx.Exec(`INSERT INTO strategy_candidates (id, strategy_set_id, agent_run_id, draft_index, strategy_type, title, description, ranking, preferred_by_agent,
  rationale, state_refs, evidence_refs, knowledge_refs, action_type, to_recipients, cc_recipients, subject, full_action_artifact, preview, eval_bundle_id,
  action_class, five_questions)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11::text[], $12::jsonb, $13::uuid[], $14, $15::jsonb, $16::jsonb, $17, $18::jsonb, $19, $20::uuid, $21, $22::jsonb)`,
		s.Candidates[i], s.SetID, s.RunID, i+1, unquote(c["strategy_type"]), unquote(c["title"]), unquote(c["description"]), rank, preferred,
		unquote(c["rationale"]), signalstore.UUIDArray(stateRefs), string(c["evidence_refs"]), signalstore.UUIDArray(knowledge), unquote(c["action_type"]),
		string(c["to"]), string(c["cc"]), subject, string(c["full_action_artifact"]), unquote(c["preview"]), s.Bundles[i],
		unquote(c["action_class"]), string(c["five_questions"]))
	if err != nil {
		return fmt.Errorf("candidate %d: %w", i+1, err)
	}
	return nil
}

// MakeBlocking turns the first eval of the candidate into a blocking failure, for refusal tests.
func MakeBlocking(t testing.TB, db *sql.DB, bundleID string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE eval_bundles SET items = jsonb_set(jsonb_set(jsonb_set(items, '{0,verdict}', '"fail"'), '{0,result,verdict}', '"fail"'), '{0,result,blocking}', 'true')
 WHERE id = $1::uuid`, bundleID); err != nil {
		t.Fatalf("strategytest: make blocking: %v", err)
	}
}

// SeedInference writes the judgment inference for the decision of the episode, as the worker will after the
// send: it repeats the human's actual choice and Ghost's preference.
func SeedInference(t testing.TB, db *sql.DB, s Seeded) string {
	t.Helper()
	inf, err := decodeObject(remap(Example(t, "judgment_inference"), map[string]string{exActivity: s.ActivityID, exMarco: s.Marco}))
	if err != nil {
		t.Fatal(err)
	}
	var id string
	err = db.QueryRow(`
INSERT INTO judgment_inferences (decision_episode_id, human_strategy_decision_id, agent_preference, human_choice, agreement, inferred_statement, semantic_labels, evidence, generated_at, model)
SELECT h.decision_episode_id, h.id, h.original_agent_preference, h.selected_candidate_id,
       CASE WHEN h.original_agent_preference = h.selected_candidate_id THEN 'agreed' ELSE 'overrode' END,
       $2, ARRAY['reduced_pressure', 'smaller_ask'], $3::jsonb, now(), 'seed'
FROM human_strategy_decisions h WHERE h.decision_episode_id = $1::uuid RETURNING id::text`,
		s.EpisodeID, unquote(mustField(t, inf, "inferred_semantic_delta", "statement")), string(inf["evidence"])).Scan(&id)
	if err != nil {
		t.Fatalf("strategytest: seed inference: %v", err)
	}
	return id
}

func mustField(t testing.TB, m raw, key, inner string) json.RawMessage {
	t.Helper()
	sub, err := decodeObject(m[key])
	if err != nil {
		t.Fatal(err)
	}
	return sub[inner]
}
