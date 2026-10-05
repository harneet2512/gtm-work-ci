package orchestrator_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
	"github.com/harneet2512/gtm-work/core-go/internal/transitionstore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// withTransition gives the scene's account a StateTransition at the replay clock: a transitions row (with its
// history snapshot at the state version) and the matching summary on the AccountState of that version, exactly
// what the detector writes in the recompute transaction. It restores both when the test ends.
func withTransition(t *testing.T, sc scene, status string) transitions.Record {
	t.Helper()
	return withTransitionRoute(t, sc, status, "REORG", "EXPANSION")
}

// withTransitionRoute is withTransition for a transition from `from` to `to`.
func withTransitionRoute(t *testing.T, sc scene, status, from, to string) transitions.Record {
	t.Helper()
	var version int
	var original []byte
	if err := env.DB.QueryRow(`SELECT version, state FROM state_history WHERE account_id = $1::uuid AND as_of <= $2
 ORDER BY as_of DESC, version DESC LIMIT 1`, sc.AccountID, sc.EventTime).Scan(&version, &original); err != nil {
		t.Fatal(err)
	}
	at := sc.EventTime.Add(-time.Minute)
	fact := func(key string, required, satisfied bool) transitions.FactResult {
		f := transitions.FactResult{Key: key, Description: "fact " + key, Required: required, Satisfied: satisfied, EvidenceRefs: []transitions.Ref{}, SignalIDs: []string{}}
		if satisfied {
			f.EvidenceRefs = []transitions.Ref{{ActivityID: sc.Activity}}
		}
		return f
	}
	rec := transitions.Record{AccountID: sc.AccountID, FromState: from, ToStateCandidate: &to, Status: status,
		TriggerActivityIDs: []string{sc.Activity}, SupportingFacts: []transitions.FactResult{fact("expansion_need_stated", true, true)},
		MissingFacts: []transitions.FactResult{fact("owner_stabilized", true, false)}, ContradictingFacts: []transitions.FactResult{},
		Confidence: 0.5, StateVersion: version, RuleSetVersion: "transition_rules:v1", FirstObservedAt: at, LastUpdatedAt: at}
	if status == transitions.StatusConfirmed {
		rec.ConfirmedAt, rec.MissingFacts = &at, []transitions.FactResult{}
		rec.SupportingFacts = append(rec.SupportingFacts, fact("owner_stabilized", true, true))
		rec.Confidence = 1
	}
	stored, err := transitionstore.Save(bg, env.DB, rec)
	if err != nil {
		t.Fatal(err)
	}
	patch := map[string]any{}
	if status == transitions.StatusConfirmed {
		patch["relationship_state"] = map[string]any{"value": to, "transition_id": stored.ID, "confirmed_at": at}
	} else {
		patch["open_transition"] = map[string]any{"transition_id": stored.ID, "from_state": from, "to_state_candidate": to, "status": status,
			"confidence": 0.5, "missing_facts": []map[string]any{{"key": "owner_stabilized", "required": true}}, "last_updated_at": at}
	}
	raw, _ := json.Marshal(patch)
	if _, err := env.DB.Exec(`UPDATE state_history SET state = state || $3::jsonb WHERE account_id = $1::uuid AND version = $2`, sc.AccountID, version, string(raw)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := env.DB.Exec(`UPDATE state_history SET state = $3::jsonb WHERE account_id = $1::uuid AND version = $2`, sc.AccountID, version, string(original)); err != nil {
			t.Error(err)
		}
		for _, stmt := range []string{`DELETE FROM state_transition_history WHERE transition_id = '` + stored.ID + `'::uuid`,
			`DELETE FROM state_transitions WHERE id = '` + stored.ID + `'::uuid`} {
			if err := storetest.Purge(bg, env.DB, stmt); err != nil {
				t.Error(err)
			}
		}
	})
	return stored
}

// expansionFirst makes the worker's top-ranked strategy an expansion motion (the move the CANDIDATE policy restricts).
func expansionFirst(f *fakeWorker) {
	f.build = func(int) []workerclient.Candidate {
		cs := f.threeCandidates()
		cs[0] = f.candidate(1, "expand_to_new_teams", "EXPANSION_MOTION", "send_email", []string{f.sc.Contact1}, nil,
			"We would love to extend the rollout to your other teams; shall we plan it this month?")
		return cs
	}
}

func candidateRows(t *testing.T, runID string) map[string]map[string]string {
	t.Helper()
	rows, err := env.DB.Query(`SELECT c.strategy_type, c.ranking::text, c.preferred_by_agent::text, COALESCE(b.candidate_policy ->> 'status', ''),
 COALESCE(b.candidate_policy::text, ''), COALESCE(b.selected_eval_suite, '') FROM strategy_candidates c JOIN eval_bundles b ON b.id = c.eval_bundle_id
 WHERE c.agent_run_id = $1::uuid`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var typ, rank, pref, status, policy, suite string
		if err := rows.Scan(&typ, &rank, &pref, &status, &policy, &suite); err != nil {
			t.Fatal(err)
		}
		out[typ] = map[string]string{"rank": rank, "preferred": pref, "policy": status, "policy_doc": policy, "suite": suite}
	}
	return out
}

func TestACandidateTransitionBlocksAnExpansionCTAFromBeingPreferred(t *testing.T) {
	sc := newScene(t)
	rec := withTransition(t, sc, transitions.StatusCandidate)
	fw := newFake(sc)
	expansionFirst(fw)
	out := mustRun(t, service(t, fw), sc.RunID)

	got := candidateRows(t, sc.RunID)
	exp := got["expand_to_new_teams"]
	if exp["policy"] != "restricted" || exp["preferred"] == "true" || exp["rank"] == "1" {
		t.Fatalf("an expansion CTA under a CANDIDATE transition must be restricted and never preferred: %v", exp)
	}
	var policy map[string]any
	if err := json.Unmarshal([]byte(exp["policy_doc"]), &policy); err != nil {
		t.Fatal(err)
	}
	if policy["transition_status"] != "CANDIDATE" || policy["requires_human_review"] != true || !slices.Contains(toStrings(policy["reasons"]), "expansion_motion") {
		t.Fatalf("the bundle must say why: %v", policy)
	}
	for typ, row := range got {
		if typ != "expand_to_new_teams" && row["policy"] != "allowed" {
			t.Fatalf("%s should be allowed: %v", typ, row)
		}
		if row["suite"] != "expansion_candidate" {
			t.Fatalf("%s bundle suite = %q", typ, row["suite"])
		}
	}
	if scalar(t, `SELECT strategy_type FROM strategy_candidates WHERE id = $1::uuid`, out.PreferredID) == "expand_to_new_teams" {
		t.Fatal("the expansion CTA is the preferred candidate")
	}
	// the transition reaches the worker (read, never rewritten) and the episode stores it
	var sent map[string]any
	if err := json.Unmarshal(fw.requests[0].StateTransition, &sent); err != nil || sent["id"] != rec.ID || sent["status"] != "CANDIDATE" {
		t.Fatalf("the worker was sent %v (%v)", sent, err)
	}
	ep := scalar(t, `SELECT transition_status || '|' || selected_eval_suite || '|' || (state_transition ->> 'id') || '|' ||
 jsonb_array_length(supporting_evidence)::text || '|' || jsonb_array_length(missing_evidence)::text || '|' || (missing_evidence -> 0 ->> 'fact_key')
 FROM decision_episodes WHERE id = $1::uuid`, out.EpisodeID)
	if ep != "CANDIDATE|expansion_candidate|"+rec.ID+"|1|1|owner_stabilized" {
		t.Fatalf("episode transition fields = %q", ep)
	}
	validEpisode(t, out.EpisodeID, sc)
	// routing: the judge saw the CANDIDATE suite and the evals of the CONFIRMED suite are excluded
	req := fw.judges[0]
	if req.EvalSuite == nil || req.EvalSuite.Name != "expansion_candidate" || !slices.Contains(req.EvalSuite.ExcludedEvalTypes, "champion_strength") {
		t.Fatalf("judge request suite = %+v", req.EvalSuite)
	}
	if slices.Contains(req.EvalSuite.ExcludedEvalTypes, "expansion_readiness") {
		t.Fatal("an eval of the selected suite must not be excluded")
	}
}

func TestAConfirmedTransitionAllowsTheExpansionCTA(t *testing.T) {
	sc := newScene(t)
	rec := withTransition(t, sc, transitions.StatusConfirmed)
	fw := newFake(sc)
	expansionFirst(fw)
	out := mustRun(t, service(t, fw), sc.RunID)

	exp := candidateRows(t, sc.RunID)["expand_to_new_teams"]
	if exp["policy"] != "allowed" || exp["rank"] != "1" || exp["preferred"] != "true" || exp["suite"] != "expansion_confirmed" {
		t.Fatalf("under a CONFIRMED transition the expansion CTA stays preferred and allowed: %v", exp)
	}
	if got := scalar(t, `SELECT transition_status || '|' || (state_transition ->> 'id') FROM decision_episodes WHERE id = $1::uuid`, out.EpisodeID); got != "CONFIRMED|"+rec.ID {
		t.Fatalf("episode = %q", got)
	}
	if fw.judges[0].EvalSuite.Name != "expansion_confirmed" || !slices.Contains(fw.judges[0].EvalSuite.ExcludedEvalTypes, "evidence_sufficiency") {
		t.Fatalf("judge suite = %+v", fw.judges[0].EvalSuite)
	}
}

func TestWithoutATransitionNoPolicyAndNoSuiteApply(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	expansionFirst(fw)
	out := mustRun(t, service(t, fw), sc.RunID)
	exp := candidateRows(t, sc.RunID)["expand_to_new_teams"]
	if exp["policy"] != "" || exp["rank"] != "1" || exp["suite"] != "" {
		t.Fatalf("no transition, no verdict and no suite: %v", exp)
	}
	if scalar(t, `SELECT coalesce(transition_status, 'none') FROM decision_episodes WHERE id = $1::uuid`, out.EpisodeID) != "none" || fw.judges[0].EvalSuite != nil {
		t.Fatal("the episode and the judge request must carry no transition")
	}
	if len(fw.requests[0].StateTransition) != 0 {
		t.Fatal("no transition must be sent when the account has none")
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// validEpisode checks the episode row against decision_episode.v1.json (the columns the contract names).
func validEpisode(t *testing.T, episodeID string, sc scene) {
	t.Helper()
	doc := scalar(t, `SELECT jsonb_build_object('id', id, 'agent_run_id', agent_run_id, 'account_id', account_id, 'status', status,
 'state_version', state_version, 'trigger_activity_ids', to_jsonb(ARRAY['`+sc.Activity+`'::uuid]), 'eval_result_ids', '[]'::jsonb,
 'customer_reaction_ids', '[]'::jsonb, 'business_outcome_ids', '[]'::jsonb, 'created_at', created_at,
 'state_transition', state_transition, 'transition_status', transition_status, 'supporting_evidence', supporting_evidence,
 'missing_evidence', missing_evidence, 'selected_eval_suite', selected_eval_suite)::text FROM decision_episodes WHERE id = $1::uuid`, episodeID)
	validDoc(t, "decision_episode", []byte(doc))
}
