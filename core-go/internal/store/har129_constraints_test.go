package store_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// HAR-129 demo-object invariants enforced by migrations 0019 and 0020 (ADR-0017).
const (
	diff129   = "d1d10000-0000-4000-8000-000000000001"
	change129 = "ac000000-0000-4000-8000-000000000001"
	bi129     = "b1000000-0000-4000-8000-000000000001"
	epi129    = "e9000000-0000-4000-8000-000000000001"
	set129    = "5e700000-0000-4000-8000-000000000001"
	hsd129    = "d5d00000-0000-4000-8000-000000000001"
	dec129    = "dec00000-0000-4000-8000-000000000001"
	event129  = "e7e00000-0000-4000-8000-000000000001"
	evidence  = `'[{"activity_id":"a0000000-0000-4000-8000-000000000001"}]'`
)

func accountRef(version int) string {
	return fmt.Sprintf(`jsonb_build_object('account_id', $ACCOUNT::text, 'version', %d)`, version)
}

func cand129(n int) string   { return fmt.Sprintf("ca000000-0000-4000-8000-00000000000%d", n) }
func bundle129(n int) string { return fmt.Sprintf("eb000000-0000-4000-8000-00000000000%d", n) }

// strategyWorld returns the statements that build a run with 3 strategy drafts, a material change, a BI update,
// an awaiting_choice episode, 3 eval bundles, a strategy set and `candidates` candidates.
const fiveQuestions = `{"what_changed":"a","why_state_changed":"b","what_remains_unknown":"c","prior_knowledge_applies":"d","why_next_action":"e"}`

func strategyWorldRaw(candidates int) []string {
	stmts := []string{
		`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ($RUN,1,'strategy_generator','{}'),($RUN,2,'strategy_generator','{}'),($RUN,3,'strategy_generator','{}')`,
		`INSERT INTO state_history (account_id, version, as_of, state) VALUES ($ACCOUNT,1,now(),'{}'),($ACCOUNT,2,now(),'{}')`,
		`INSERT INTO state_diffs (id, account_id, from_version, to_version, is_material, changes) VALUES ('` + diff129 + `',$ACCOUNT,1,2,true,'[{"field":"blockers","op":"changed","material":true}]')`,
		`INSERT INTO account_changes (id, account_id, opportunity_id, held_out_event_id, trigger_activity_ids, previous_state_ref, current_state_ref, state_diff_id, graph_diff_ref, material_change, evidence_refs)
		 VALUES ('` + change129 + `',$ACCOUNT,$OPP,'` + event129 + `',ARRAY[$ACTIVITY]::uuid[],` + accountRef(1) + `,` + accountRef(2) + `,'` + diff129 + `','{}',true,` + evidence + `)`,
		`INSERT INTO business_intelligence_updates (id, account_id, opportunity_id, account_change_id, summary, claims, why_it_matters, account_map_ref)
		 VALUES ('` + bi129 + `',$ACCOUNT,$OPP,'` + change129 + `','s','[{"statement":"x","dimension":"blockers_risk","evidence_refs":[{"activity_id":"a0000000-0000-4000-8000-000000000001"}]}]','w','{}')`,
		`INSERT INTO decision_episodes (id, agent_run_id, account_id, state_version, status, held_out_event_id, account_change_id, business_intelligence_update_id)
		 VALUES ('` + epi129 + `',$RUN,$ACCOUNT,2,'awaiting_choice','` + event129 + `','` + change129 + `','` + bi129 + `')`,
		`INSERT INTO strategy_sets (id, decision_episode_id, agent_run_id, account_id, opportunity_id, generated_at, state_ref, trigger_activity_ids)
		 VALUES ('` + set129 + `','` + epi129 + `',$RUN,$ACCOUNT,$OPP,now(),'{}',ARRAY[$ACTIVITY]::uuid[])`,
	}
	for n := 1; n <= 3; n++ {
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO eval_bundles (id, agent_run_id, draft_index, items, generated_at)
		 VALUES ('%s',$RUN,%d,jsonb_build_array(jsonb_build_object('eval_type','grounding','verdict','pass','result',jsonb_build_object('agent_run_id',$RUN::text,'draft_index',%d))),now())`, bundle129(n), n, n))
	}
	for n := 1; n <= candidates; n++ {
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO strategy_candidates (id, strategy_set_id, agent_run_id, draft_index, strategy_type, title, description, ranking,
		   preferred_by_agent, rationale, evidence_refs, action_type, to_recipients, subject, full_action_artifact, preview, eval_bundle_id,
		   action_class, five_questions)
		 VALUES ('%s','%s',$RUN,%d,'type_%d','t','d',%d,%t,'r',%s,'send_email','[{"person_id":"p","role":"to"}]','Re: x','{"channel":"email","subject":"Re: x","body":"b"}','p','%s',
		   'REPLY','%s')`,
			cand129(n), set129, n, n, n, n == 1, evidence, bundle129(n), fiveQuestions))
	}
	return stmts
}

// strategyWorld is the world with the deferred size check forced to run.
func strategyWorld(candidates int) []string {
	return append(strategyWorldRaw(candidates), `SET CONSTRAINTS ALL IMMEDIATE`)
}

func runWorld(t *testing.T, tx *sql.Tx, ids seededIDs, stmts []string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := tx.Exec(ids.expand(s)); err != nil {
			t.Fatalf("setup: %v\n%s", err, s)
		}
	}
}

func chooseSQL(selected string) string { return chooseWith(selected, cand129(1)) }

func chooseWith(selected, preferred string) string {
	return fmt.Sprintf(`INSERT INTO human_strategy_decisions (id, decision_episode_id, agent_run_id, strategy_set_id, selected_candidate_id, original_agent_preference, surface, actor_label, chosen_at)
	 VALUES ('%s','%s',$RUN,'%s','%s','%s','slack','Dana',now())`, hsd129, epi129, set129, selected, preferred)
}

const humanDecisionSQL = `INSERT INTO human_decisions (id, agent_run_id, decision, surface, actor_label, edited_artifact) VALUES ('` + dec129 + `',$RUN,'%s','slack','Dana',%s)`

// inferenceSQL inserts an inference for the choice made in chooseSQL; cols and vals add columns.
func inferenceSQL(preference, choice, agreement, verdict, cols, vals string) string {
	return fmt.Sprintf(`INSERT INTO judgment_inferences (decision_episode_id, human_strategy_decision_id, agent_preference, human_choice, agreement, inferred_statement, evidence, human_verdict, generated_at%s)
	 VALUES ('%s','%s','%s','%s','%s','why','{"evidence_refs":[{"activity_id":"a0000000-0000-4000-8000-000000000001"}]}','%s',now()%s)`,
		cols, epi129, hsd129, preference, choice, agreement, verdict, vals)
}

var har129Cases = []struct {
	name    string
	sql     string
	wantErr string
}{
	{"a candidate's draft must exist", `UPDATE strategy_candidates SET draft_index = 9 WHERE id = '` + cand129(3) + `'`, foreignKeyViolation},
	{"ranking is unique within a set", `UPDATE strategy_candidates SET ranking = 3 WHERE id = '` + cand129(2) + `'`, uniqueViolation},
	{"ghost prefers exactly its first-ranked candidate", `UPDATE strategy_candidates SET preferred_by_agent = true WHERE id = '` + cand129(2) + `'`, checkViolation},
	{"a demo set ranks 1 to 3", `UPDATE strategy_candidates SET ranking = 4 WHERE id = '` + cand129(3) + `'`, checkViolation},
	{"card subject equals the artifact subject", `UPDATE strategy_candidates SET subject = 'Other' WHERE id = '` + cand129(2) + `'`, checkViolation},
	{"an email candidate needs a recipient", `UPDATE strategy_candidates SET to_recipients = '[]' WHERE id = '` + cand129(2) + `'`, checkViolation},
	{"a candidate must cite evidence", `UPDATE strategy_candidates SET evidence_refs = '[]' WHERE id = '` + cand129(2) + `'`, checkViolation},
	{"strategy type is lower snake case", `UPDATE strategy_candidates SET strategy_type = 'Stronger CTA' WHERE id = '` + cand129(2) + `'`, checkViolation},
	{"a decision class is carried out by its own tool action", `UPDATE strategy_candidates SET action_class = 'WAIT' WHERE id = '` + cand129(2) + `'`, checkViolation},
	{"an unknown decision class", `UPDATE strategy_candidates SET action_class = 'PUSH' WHERE id = '` + cand129(2) + `'`, checkViolation},
	{"a policy reason comes from the closed set", `UPDATE eval_bundles SET candidate_policy = '{"transition_status":"CANDIDATE","status":"restricted","reasons":["vibes"],"requires_human_review":true}' WHERE id = '` + bundle129(2) + `'`, checkViolation},
	{"a restricted policy requires human review", `UPDATE eval_bundles SET candidate_policy = '{"transition_status":"CANDIDATE","status":"restricted","reasons":["pricing_push"],"requires_human_review":false}' WHERE id = '` + bundle129(2) + `'`, checkViolation},
	{"a restricted policy states that human review is required", `UPDATE eval_bundles SET candidate_policy = '{"transition_status":"CANDIDATE","status":"restricted","reasons":["pricing_push"]}' WHERE id = '` + bundle129(2) + `'`, checkViolation},
	{"a restricted policy has a reason", `UPDATE eval_bundles SET candidate_policy = '{"transition_status":"CANDIDATE","status":"restricted","reasons":[],"requires_human_review":true}' WHERE id = '` + bundle129(2) + `'`, checkViolation},
	{"an allowed policy has no reasons", `UPDATE eval_bundles SET candidate_policy = '{"transition_status":"CANDIDATE","status":"allowed","reasons":["pricing_push"],"requires_human_review":false}' WHERE id = '` + bundle129(2) + `'`, checkViolation},
	{"the five questions are all answered", `UPDATE strategy_candidates SET five_questions = jsonb_set(five_questions, '{why_next_action}', '""') WHERE id = '` + cand129(2) + `'`, checkViolation},
	{"the five questions are exactly five", `UPDATE strategy_candidates SET five_questions = five_questions || '{"extra":"x"}' WHERE id = '` + cand129(2) + `'`, checkViolation},
	{"a candidate has one eval bundle", `UPDATE strategy_candidates SET eval_bundle_id = '` + bundle129(1) + `' WHERE id = '` + cand129(2) + `'`, uniqueViolation},
	{"one strategy set per run", `INSERT INTO strategy_sets (decision_episode_id, agent_run_id, account_id, generated_at, state_ref, trigger_activity_ids) VALUES ('` + epi129 + `',$RUN,$ACCOUNT,now(),'{}',ARRAY[$ACTIVITY]::uuid[])`, uniqueViolation},
	{"a bundle's results belong to its draft", `INSERT INTO eval_bundles (agent_run_id, draft_index, items, generated_at) VALUES ($RUN,1,jsonb_build_array(jsonb_build_object('result', jsonb_build_object('agent_run_id', $RUN::text, 'draft_index', 2))),now())`, checkViolation},
	{"a change's snapshots are account-level", `UPDATE account_changes SET previous_state_ref = jsonb_build_object('account_id', $ACCOUNT::text, 'opportunity_id', $OPP::text, 'version', 1)`, checkViolation},
	{"a change's snapshots match the diff's versions", `UPDATE account_changes SET current_state_ref = ` + accountRef(5), checkViolation},
	{"an episode cannot be chosen without a strategy decision", `UPDATE decision_episodes SET status = 'chosen' WHERE id = '` + epi129 + `'`, checkViolation},
	{"an episode cannot go back", chooseSQL(cand129(2)) + `; UPDATE decision_episodes SET status = 'chosen' WHERE id = '` + epi129 + `'; UPDATE decision_episodes SET status = 'awaiting_choice' WHERE id = '` + epi129 + `'`, checkViolation},
	{"an episode is judged only after the human answered", chooseSQL(cand129(2)) + `;` + inferenceSQL(cand129(1), cand129(2), "overrode", "pending", "", "") + `; INSERT INTO human_decisions (id, agent_run_id, decision, surface, actor_label) VALUES ('` + dec129 + `',$RUN,'approve','slack','Dana'); UPDATE decision_episodes SET status = 'judged', human_decision_id = '` + dec129 + `', human_action = 'APPROVE_UNCHANGED', final_draft_index = 1 WHERE id = '` + epi129 + `'`, checkViolation},
	{"a strategy draft is not a revision", `INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output, revision_feedback) VALUES ($RUN,4,'strategy_generator','{}','[{"instruction":"x"}]')`, checkViolation},
	{"a change's material flag equals its diff's", `INSERT INTO account_changes (account_id, trigger_activity_ids, previous_state_ref, current_state_ref, state_diff_id, graph_diff_ref, material_change) VALUES ($ACCOUNT,ARRAY[$ACTIVITY]::uuid[],` + accountRef(1) + `,` + accountRef(2) + `,'` + diff129 + `','{}',false)`, foreignKeyViolation},
	{"a material change must be traceable", `UPDATE account_changes SET evidence_refs = '[]' WHERE id = '` + change129 + `'`, checkViolation},
	{"every BI claim has evidence", `UPDATE business_intelligence_updates SET claims = '[{"statement":"x","dimension":"blockers_risk","evidence_refs":[]}]' WHERE id = '` + bi129 + `'`, checkViolation},
	{"a BI claim carries no confidence score", `UPDATE business_intelligence_updates SET claims = '[{"statement":"x","confidence":0.9,"evidence_refs":[{"activity_id":"a"}]}]' WHERE id = '` + bi129 + `'`, checkViolation},
	{"an undecided episode has no decision or action", `UPDATE decision_episodes SET human_action = 'REJECT' WHERE id = '` + epi129 + `'`, checkViolation},
	{"a decided episode needs its decision", `UPDATE decision_episodes SET status = 'decided' WHERE id = '` + epi129 + `'`, checkViolation},
	// HAR-139 (0027): the human delta link rules.
	{"an edited action needs its human delta",
		chooseSQL(cand129(2)) + `; UPDATE decision_episodes SET status = 'chosen' WHERE id = '` + epi129 + `';` +
			fmt.Sprintf(humanDecisionSQL, "edit", `'{"channel":"email","body":"b"}'`) +
			`; UPDATE decision_episodes SET status = 'decided', human_decision_id = '` + dec129 + `', human_action = 'APPROVE_WITH_EDIT',
			   final_draft_index = 2, human_final_artifact = '{}' WHERE id = '` + epi129 + `'`, checkViolation},
	{"only an edited or replaced action carries a delta",
		`INSERT INTO human_deltas (id, decision_episode_id, unexplained, candidate_criterion)
		 VALUES ('dd000000-0000-4000-8000-0000000000d1','` + epi129 + `',true,'{"statement":"s","suggested_eval_type":"cta_calibration"}');` +
			chooseSQL(cand129(2)) + `;` + fmt.Sprintf(humanDecisionSQL, "approve", `NULL`) +
			`; UPDATE decision_episodes SET status = 'decided', human_decision_id = '` + dec129 + `', human_action = 'APPROVE_UNCHANGED',
			   final_draft_index = 1, human_delta_id = 'dd000000-0000-4000-8000-0000000000d1' WHERE id = '` + epi129 + `'`, checkViolation},
	{"a human delta needs a decided episode",
		`INSERT INTO human_deltas (id, decision_episode_id, unexplained, candidate_criterion)
		 VALUES ('dd000000-0000-4000-8000-0000000000d1','` + epi129 + `',true,'{"statement":"s","suggested_eval_type":"cta_calibration"}');
		 UPDATE decision_episodes SET human_delta_id = 'dd000000-0000-4000-8000-0000000000d1' WHERE id = '` + epi129 + `'`, checkViolation},
	{"a human delta's labels come from the vocabulary",
		`INSERT INTO human_deltas (decision_episode_id, unexplained, semantic_labels, candidate_criterion)
		 VALUES ('` + epi129 + `',true,'{made_up_label}','{"statement":"s","suggested_eval_type":"cta_calibration"}')`, checkViolation},
	{"a replay episode needs its change", `UPDATE decision_episodes SET account_change_id = NULL WHERE id = '` + epi129 + `'`, checkViolation},
	{"the preference recorded is the first-ranked candidate", chooseWith(cand129(2), cand129(2)), checkViolation},
	{"the selection must belong to the set", chooseSQL("cafe0000-0000-4000-8000-000000000000"), foreignKeyViolation},
	{"a pending choice has no send time", chooseSQL(cand129(2)) + `; UPDATE human_strategy_decisions SET send_decided_at = now()`, checkViolation},
	{"edits need the final artifact", chooseSQL(cand129(2)) + `; UPDATE human_strategy_decisions SET edits = '[{"kind":"subject_changed"}]'`, checkViolation},
	{"the selection cannot change", chooseSQL(cand129(2)) + `; UPDATE human_strategy_decisions SET selected_candidate_id = '` + cand129(3) + `'`, checkViolation},
	{"send needs the final artifact, recipients and decision", chooseSQL(cand129(2)) + `; UPDATE human_strategy_decisions SET send_decision = 'send', send_decided_at = now()`, checkViolation},
	{"an unedited send records an approve", fmt.Sprintf(humanDecisionSQL, "edit", `'{"channel":"email","body":"b"}'`) + `;` + chooseSQL(cand129(2)) + `; UPDATE human_strategy_decisions SET send_decision = 'send', send_decided_at = now(), final_to = '[{"person_id":"p"}]', final_artifact = '{"body":"b"}', human_decision_id = '` + dec129 + `'`, checkViolation},
	{"an edited send records an edit", fmt.Sprintf(humanDecisionSQL, "approve", "NULL") + `;` + chooseSQL(cand129(2)) + `; UPDATE human_strategy_decisions SET edits = '[{"kind":"subject_changed"}]', send_decision = 'send', send_decided_at = now(), final_to = '[{"person_id":"p"}]', final_artifact = '{"body":"b"}', human_decision_id = '` + dec129 + `'`, checkViolation},
	{"a send decision is final", fmt.Sprintf(humanDecisionSQL, "approve", "NULL") + `;` + chooseSQL(cand129(2)) + `; UPDATE human_strategy_decisions SET send_decision = 'send', send_decided_at = now(), final_to = '[{"person_id":"p"}]', final_artifact = '{"body":"b"}', human_decision_id = '` + dec129 + `'; UPDATE human_strategy_decisions SET actor_label = 'Other'`, checkViolation},
	{"agreement matches the two ids", chooseSQL(cand129(2)) + `;` + inferenceSQL(cand129(1), cand129(2), "agreed", "pending", "", ""), checkViolation},
	{"an inference repeats the human's actual choice", chooseSQL(cand129(2)) + `;` + inferenceSQL(cand129(1), cand129(3), "overrode", "pending", "", ""), foreignKeyViolation},
	{"a pending inference has no verdict time", chooseSQL(cand129(2)) + `;` + inferenceSQL(cand129(1), cand129(2), "overrode", "pending", ", verdict_at", ", now()"), checkViolation},
	{"a correction carries the corrected statement", chooseSQL(cand129(2)) + `;` + inferenceSQL(cand129(1), cand129(2), "overrode", "corrected", ", verdict_at, verdict_surface", ", now(), 'slack'"), checkViolation},
	{"a confirmed inference has nothing to correct", chooseSQL(cand129(2)) + `;` + inferenceSQL(cand129(1), cand129(2), "overrode", "confirmed", ", verdict_at, verdict_surface, corrected_statement", ", now(), 'slack', 'c'"), checkViolation},
	{"an inference needs account-state evidence", chooseSQL(cand129(2)) + `; INSERT INTO judgment_inferences (decision_episode_id, human_strategy_decision_id, agent_preference, human_choice, agreement, inferred_statement, evidence, generated_at) VALUES ('` + epi129 + `','` + hsd129 + `','` + cand129(1) + `','` + cand129(2) + `','overrode','why','{"evidence_refs":[]}',now())`, checkViolation},
	{"semantic labels come from the human delta vocabulary", chooseSQL(cand129(2)) + `;` + inferenceSQL(cand129(1), cand129(2), "overrode", "pending", ", semantic_labels", ", '{vibes}'"), checkViolation},
}

func TestHAR129Constraints(t *testing.T) {
	for _, tc := range har129Cases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
				runWorld(t, tx, ids, strategyWorld(3))
				assertSQLState(t, execExpectingError(tx, ids.expand(tc.sql)), tc.wantErr)
			})
		})
	}
}

func TestAWellFormedCandidatePolicyAndTheNoAcceptableCandidateFlagAreStored(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorld(3))
		for _, q := range []string{
			`UPDATE eval_bundles SET candidate_policy = '{"transition_status":"CANDIDATE","status":"restricted","reasons":["expansion_motion","expansion_label_mismatch"],"requires_human_review":true}' WHERE id = '` + bundle129(2) + `'`,
			`UPDATE eval_bundles SET candidate_policy = '{"transition_status":"CONFIRMED","status":"allowed","reasons":[]}' WHERE id = '` + bundle129(3) + `'`,
			`UPDATE strategy_sets SET no_acceptable_candidate = true WHERE id = '` + set129 + `'`,
		} {
			if _, err := tx.Exec(ids.expand(q)); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		var flag bool
		if err := tx.QueryRow(`SELECT no_acceptable_candidate FROM strategy_sets WHERE id = '` + set129 + `'`).Scan(&flag); err != nil || !flag {
			t.Fatalf("flag %v err %v", flag, err)
		}
	})
}

func TestStrategySetHoldsExactlyThreeCandidates(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorldRaw(2))
		assertSQLState(t, execExpectingError(tx, `SET CONSTRAINTS ALL IMMEDIATE`), checkViolation)
	})
}

func TestDemoManifestRules(t *testing.T) {
	history := `'[{"event":{"event_id":"e7e00000-0000-4000-8000-000000000001","replay_position":1}}]'`
	held := func(extra string) string {
		return `'{"event_id":"e7e00000-0000-4000-8000-000000000002","replay_position":2,"payload_sha256":"` + strings.Repeat("c", 64) + `"` + extra + `}'`
	}
	insert := func(hist, heldOut, sha string) string {
		return `INSERT INTO demo_manifests (account_id, opportunity_id, data_cutoff, events, held_out_event, why_selected, content_sha256) VALUES ($ACCOUNT,$OPP,now(),` + hist + `,` + heldOut + `,'why','` + sha + `')`
	}
	good := strings.Repeat("a", 64)
	cases := []struct{ name, sql, want string }{
		{"the held-out event follows the history", insert(history, `'{"event_id":"e7e00000-0000-4000-8000-000000000002","replay_position":5,"payload_sha256":"`+strings.Repeat("c", 64)+`"}'`, good), checkViolation},
		{"event N carries no state snapshot", insert(history, held(`,"state_after":{}`), good), checkViolation},
		{"event N carries no diff", insert(history, held(`,"state_diff_id":null`), good), checkViolation},
		{"event N carries no material flag", insert(history, held(`,"is_material":true`), good), checkViolation},
		{"a manifest has history", insert(`'[]'`, held(""), good), checkViolation},
		{"the content hash is sha256 hex", insert(history, held(""), "xyz"), checkViolation},
		{"the opportunity belongs to the account", strings.Replace(insert(history, held(""), good), "$OPP", "'cafe0000-0000-4000-8000-000000000000'", 1), foreignKeyViolation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
				assertSQLState(t, execExpectingError(tx, ids.expand(tc.sql)), tc.want)
			})
		})
	}
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		if _, err := tx.Exec(ids.expand(insert(history, held(""), good))); err != nil {
			t.Fatalf("valid manifest rejected: %v", err)
		}
	})
}

// The whole chain: choose, edit, send, decide, infer, answer, with the episode closing in stages.
func TestHAR129HappyPathRoundTrip(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorld(3))
		runWorld(t, tx, ids, []string{
			chooseSQL(cand129(2)),
			`UPDATE decision_episodes SET status = 'chosen' WHERE id = '` + epi129 + `'`,
			fmt.Sprintf(humanDecisionSQL, "edit", `'{"channel":"email","body":"b2"}'`),
			`UPDATE human_strategy_decisions SET edits = '[{"kind":"paragraph_edited"}]', send_decision = 'send', send_decided_at = now(),
			   final_to = '[{"person_id":"p"}]', final_artifact = '{"channel":"email","body":"b2"}', human_decision_id = '` + dec129 + `'`,
			// HAR-139: an edited send writes its HumanDelta before the episode is decided with it.
			`INSERT INTO human_deltas (id, decision_episode_id, literal_changes, unexplained, candidate_criterion)
			 VALUES ('dd000000-0000-4000-8000-0000000000d1','` + epi129 + `','[{"kind":"paragraph_edited"}]',true,
			   '{"statement":"the edit softened the CTA","suggested_eval_type":"cta_calibration"}')`,
			`UPDATE decision_episodes SET status = 'decided', human_decision_id = '` + dec129 + `', human_action = 'APPROVE_WITH_EDIT',
			   final_draft_index = 2, human_final_artifact = '{"channel":"email","body":"b2"}',
			   human_delta_id = 'dd000000-0000-4000-8000-0000000000d1' WHERE id = '` + epi129 + `'`,
			inferenceSQL(cand129(1), cand129(2), "overrode", "pending", "", ""),
			`UPDATE judgment_inferences SET human_verdict = 'corrected', corrected_statement = 'c', verdict_at = now(), verdict_surface = 'slack'`,
			`UPDATE decision_episodes SET status = 'judged', learning_scope = 'account_specific' WHERE id = '` + epi129 + `'`,
		})
	})
}
