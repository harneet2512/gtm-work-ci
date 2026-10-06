package store_test

import (
	"database/sql"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// HAR-97 loop invariants enforced by migration 0007.
var har97Cases = []constraintCase{
	{"draft 1 must come from the account agent",
		`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ($RUN,1,'revision_planner','{}')`,
		checkViolation},
	{"later drafts must come from the revision planner",
		`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ($RUN,2,'account_agent','{}')`,
		checkViolation},
	{"an eval must reference an existing draft",
		`INSERT INTO eval_runs (agent_run_id, evaluator, evaluator_version, kind, verdict, draft_index, evidence_class) VALUES ($RUN,'grounding','grounding:v1','semantic','pass',5,'deal_data')`,
		foreignKeyViolation},
	{"only failures can block",
		`INSERT INTO eval_runs (agent_run_id, evaluator, evaluator_version, kind, verdict, draft_index, blocking, evidence_class) VALUES ($RUN,'grounding','grounding:v1','semantic','warn',1,true,'deal_data')`,
		checkViolation},
	{"deterministic evals never use a model",
		`INSERT INTO eval_runs (agent_run_id, evaluator, evaluator_version, kind, verdict, draft_index, model, evidence_class) VALUES ($RUN,'recipient_correctness','recipient_correctness:v1','deterministic','pass',1,'some-llm','product_rule')`,
		checkViolation},
	{"unknown evaluator is rejected",
		`INSERT INTO eval_runs (agent_run_id, evaluator, evaluator_version, kind, verdict, draft_index, evidence_class) VALUES ($RUN,'vibes','vibes:v1','semantic','pass',1,'deal_data')`,
		checkViolation},
	{"evaluator version must be '<type>:v<N>'",
		`INSERT INTO eval_runs (agent_run_id, evaluator, evaluator_version, kind, verdict, draft_index, evidence_class) VALUES ($RUN,'grounding','latest','semantic','pass',1,'deal_data')`,
		checkViolation},
	{"only one active version per evaluator",
		`INSERT INTO evaluator_versions (evaluator, version, status, kind, rubric, created_from, promoted_at) VALUES ('buyer_readiness',1,'active','semantic','r','seed',now()), ('buyer_readiness',2,'active','semantic','r','human_delta',now())`,
		uniqueViolation},
	{"an active evaluator version must record promotion",
		`INSERT INTO evaluator_versions (evaluator, version, status, kind, rubric, created_from) VALUES ('buyer_readiness',1,'active','semantic','r','seed')`,
		checkViolation},
	{"edited episode must keep the human's artifact",
		`INSERT INTO human_decisions (id, agent_run_id, decision, surface, actor_label, edited_artifact) VALUES ('11111111-1111-4111-8111-111111111111',$RUN,'edit','web','Dana','{}'); INSERT INTO decision_episodes (agent_run_id, account_id, state_version, final_draft_index, human_decision_id, human_action) VALUES ($RUN,$ACCOUNT,7,1,'11111111-1111-4111-8111-111111111111','APPROVE_WITH_EDIT')`,
		checkViolation},
	{"unexplained human delta must propose a candidate criterion",
		`INSERT INTO human_decisions (id, agent_run_id, decision, surface, actor_label) VALUES ('11111111-1111-4111-8111-111111111111',$RUN,'approve','web','Dana'); INSERT INTO decision_episodes (id, agent_run_id, account_id, state_version, final_draft_index, human_decision_id, human_action) VALUES ('22222222-2222-4222-8222-222222222222',$RUN,$ACCOUNT,7,1,'11111111-1111-4111-8111-111111111111','APPROVE_UNCHANGED'); INSERT INTO human_deltas (decision_episode_id, unexplained) VALUES ('22222222-2222-4222-8222-222222222222',true)`,
		checkViolation},
	{"explained human delta must cite the evals that predicted it",
		`INSERT INTO human_decisions (id, agent_run_id, decision, surface, actor_label) VALUES ('11111111-1111-4111-8111-111111111111',$RUN,'approve','web','Dana'); INSERT INTO decision_episodes (id, agent_run_id, account_id, state_version, final_draft_index, human_decision_id, human_action) VALUES ('22222222-2222-4222-8222-222222222222',$RUN,$ACCOUNT,7,1,'11111111-1111-4111-8111-111111111111','APPROVE_UNCHANGED'); INSERT INTO human_deltas (decision_episode_id, unexplained) VALUES ('22222222-2222-4222-8222-222222222222',false)`,
		checkViolation},
	{"knowledge keys look like K17",
		`INSERT INTO knowledge (title, guidance, key) VALUES ('t','g','knowledge-17')`,
		checkViolation},
	{"customer reaction polarity is constrained",
		`INSERT INTO customer_reactions (account_id, activity_id, reaction_type, polarity) VALUES ($ACCOUNT,$ACTIVITY,'replied','ecstatic')`,
		checkViolation},
	{"evaluator version must match its evaluator type",
		`INSERT INTO eval_runs (agent_run_id, evaluator, evaluator_version, kind, verdict, draft_index, evidence_class) VALUES ($RUN,'grounding','timing_cadence:v1','semantic','pass',1,'deal_data')`,
		checkViolation},
	{"evidence class must be stated",
		`INSERT INTO eval_runs (agent_run_id, evaluator, evaluator_version, kind, verdict, draft_index) VALUES ($RUN,'grounding','grounding:v1','semantic','pass',1)`,
		notNullViolation},
	{"evaluator status cannot skip shadow",
		`INSERT INTO evaluator_versions (evaluator, version, status, kind, rubric, created_from) VALUES ('buyer_readiness',1,'candidate','semantic','r','seed'); UPDATE evaluator_versions SET status='active', promoted_at=now() WHERE evaluator='buyer_readiness'`,
		checkViolation},
	{"retired evaluator versions stay retired",
		`INSERT INTO evaluator_versions (evaluator, version, status, kind, rubric, created_from) VALUES ('buyer_readiness',1,'retired','semantic','r','seed'); UPDATE evaluator_versions SET status='shadow' WHERE evaluator='buyer_readiness'`,
		checkViolation},
	{"only shadow versions can be promoted",
		`INSERT INTO evaluator_versions (evaluator, version, status, kind, rubric, created_from) VALUES ('buyer_readiness',1,'candidate','semantic','r','seed'); SELECT promote_evaluator_version('buyer_readiness',1)`,
		checkViolation},
	{"episode action must match the human decision",
		`INSERT INTO human_decisions (id, agent_run_id, decision, surface, actor_label) VALUES ('11111111-1111-4111-8111-111111111111',$RUN,'reject','web','Dana'); INSERT INTO decision_episodes (agent_run_id, account_id, state_version, final_draft_index, human_decision_id, human_action) VALUES ($RUN,$ACCOUNT,7,1,'11111111-1111-4111-8111-111111111111','APPROVE_UNCHANGED')`,
		checkViolation},
	{"episode account must be the run's account",
		`INSERT INTO human_decisions (id, agent_run_id, decision, surface, actor_label) VALUES ('11111111-1111-4111-8111-111111111111',$RUN,'approve','web','Dana'); INSERT INTO decision_episodes (agent_run_id, account_id, state_version, final_draft_index, human_decision_id, human_action) VALUES ($RUN,$OTHER_ACCOUNT,7,1,'11111111-1111-4111-8111-111111111111','APPROVE_UNCHANGED')`,
		foreignKeyViolation},
	{"a reaction is counted once per activity",
		`INSERT INTO customer_reactions (account_id, activity_id, reaction_type, polarity) VALUES ($ACCOUNT,$ACTIVITY,'replied','positive'), ($ACCOUNT,$ACTIVITY,'replied','positive')`,
		uniqueViolation},
	{"runs with supervision records cannot be deleted",
		`INSERT INTO human_decisions (agent_run_id, decision, surface, actor_label) VALUES ($RUN,'approve','web','Dana'); DELETE FROM agent_runs WHERE id = $RUN`,
		foreignKeyViolation},
	{"only employees can be internal-only (WP17, HAR-97 §4 recipient correctness)",
		`INSERT INTO people (kind, display_name, account_id, internal_only) VALUES ('contact','Pat',$ACCOUNT,true)`,
		checkViolation},
	{"knowledge signature must be a list of conditions",
		`INSERT INTO knowledge (title, guidance, situation_signature) VALUES ('t','g','{"motion":"expansion"}')`,
		checkViolation},
}

func TestHAR97Constraints(t *testing.T) {
	for _, tc := range har97Cases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
				if _, err := tx.Exec(ids.expand(`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ($RUN,1,'account_agent','{}')`)); err != nil {
					t.Fatalf("seed draft 1: %v", err)
				}
				err := execExpectingError(tx, ids.expand(tc.sql))
				assertSQLState(t, err, tc.wantCode)
			})
		})
	}
}

func TestHAR97HappyPathEpisodeRoundTrip(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		stmts := []string{
			`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ($RUN,1,'account_agent','{}')`,
			`INSERT INTO eval_runs (id, agent_run_id, evaluator, evaluator_version, kind, verdict, draft_index, blocking, label, evidence_class, model)
			 VALUES ('33333333-3333-4333-8333-333333333333',$RUN,'champion_continuity','champion_continuity:v1','semantic','fail',1,true,'CHAMPION_BYPASSED','deal_data','deepseek/deepseek-v4-flash')`,
			`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output, revision_feedback) VALUES ($RUN,2,'revision_planner','{}','[{"eval_result_id":"33333333-3333-4333-8333-333333333333","instruction":"keep Priya"}]')`,
			`INSERT INTO human_decisions (id, agent_run_id, decision, surface, actor_label, edited_artifact) VALUES ('11111111-1111-4111-8111-111111111111',$RUN,'edit','web','Dana','{"channel":"email","body":"x"}')`,
			// HAR-139: an edited action needs its delta, and the delta needs the episode — the episode is
			// opened pending, the delta written, then the episode decided with the link (the real order).
			`INSERT INTO decision_episodes (id, agent_run_id, account_id, state_version, status)
			 VALUES ('22222222-2222-4222-8222-222222222222',$RUN,$ACCOUNT,7,'awaiting_choice')`,
			`INSERT INTO human_deltas (id, decision_episode_id, unexplained, semantic_labels, candidate_criterion)
			 VALUES ('44444444-4444-4444-8444-444444444444','22222222-2222-4222-8222-222222222222',true,'{reduced_pressure,deferred_to_buyer_timing}','{"statement":"s","suggested_eval_type":"buyer_readiness"}')`,
			`UPDATE decision_episodes SET status = 'decided', final_draft_index = 2, human_decision_id = '11111111-1111-4111-8111-111111111111',
			   human_action = 'APPROVE_WITH_EDIT', human_final_artifact = '{"channel":"email","body":"x"}',
			   human_delta_id = '44444444-4444-4444-8444-444444444444' WHERE id = '22222222-2222-4222-8222-222222222222'`,
			`INSERT INTO knowledge (title, guidance, key, status) VALUES ('Keep champion','g','K17','candidate')`,
			`SET CONSTRAINTS ALL IMMEDIATE`,
		}
		for _, s := range stmts {
			if _, err := tx.Exec(ids.expand(s)); err != nil {
				t.Fatalf("happy path insert failed: %v\n%s", err, s)
			}
		}
	})
}

func TestEvaluatorPromotionIsAtomic(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		for _, q := range []string{
			`INSERT INTO evaluator_versions (evaluator, version, status, kind, rubric, created_from, promoted_at) VALUES ('buyer_readiness',1,'active','semantic','v1','seed',now())`,
			`INSERT INTO evaluator_versions (evaluator, version, status, kind, rubric, created_from) VALUES ('buyer_readiness',2,'shadow','semantic','v2','human_delta')`,
			`SELECT promote_evaluator_version('buyer_readiness',2)`,
		} {
			if _, err := tx.Exec(q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		var active, retired int
		err := tx.QueryRow(`SELECT max(version) FILTER (WHERE status='active'), max(version) FILTER (WHERE status='retired')
			FROM evaluator_versions WHERE evaluator='buyer_readiness'`).Scan(&active, &retired)
		if err != nil {
			t.Fatal(err)
		}
		if active != 2 || retired != 1 {
			t.Fatalf("after promotion active=v%d retired=v%d, want v2 active and v1 retired", active, retired)
		}
	})
}

func TestKnowledgeStatusChangesAreRecorded(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		var id string
		if err := tx.QueryRow(`INSERT INTO knowledge (title, guidance) VALUES ('Keep champion','g') RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`SET LOCAL ghost.knowledge_reason = 'second supporting episode'`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE knowledge SET status = 'provisional' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		var n int
		var lastReason string
		err := tx.QueryRow(`SELECT count(*), (array_agg(reason ORDER BY id DESC))[1]
			FROM knowledge_status_history WHERE knowledge_id = $1`, id).Scan(&n, &lastReason)
		if err != nil {
			t.Fatal(err)
		}
		if n != 2 || lastReason != "second supporting episode" {
			t.Fatalf("history rows=%d last reason=%q", n, lastReason)
		}
	})
}

func TestExplanationMustComeFromTheSameRun(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		stmts := []string{
			`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ($RUN,1,'account_agent','{}')`,
			`UPDATE agent_runs SET status = 'rejected' WHERE id = $RUN`,
			`INSERT INTO agent_runs (id, account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids)
			 VALUES ('44444444-4444-4444-8444-444444444444',$ACCOUNT,'post_interaction_followup','dry_run','awaiting_human',$TRIGGER2,ARRAY[$ACTIVITY]::uuid[])`,
			`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ('44444444-4444-4444-8444-444444444444',1,'account_agent','{}')`,
			`INSERT INTO eval_runs (id, agent_run_id, evaluator, evaluator_version, kind, verdict, draft_index, evidence_class)
			 VALUES ('33333333-3333-4333-8333-333333333333','44444444-4444-4444-8444-444444444444','grounding','grounding:v1','semantic','warn',1,'deal_data')`,
			`INSERT INTO human_decisions (id, agent_run_id, decision, surface, actor_label) VALUES ('11111111-1111-4111-8111-111111111111',$RUN,'approve','web','Dana')`,
			`INSERT INTO decision_episodes (id, agent_run_id, account_id, state_version, final_draft_index, human_decision_id, human_action)
			 VALUES ('22222222-2222-4222-8222-222222222222',$RUN,$ACCOUNT,7,1,'11111111-1111-4111-8111-111111111111','APPROVE_UNCHANGED')`,
		}
		for _, s := range stmts {
			if _, err := tx.Exec(ids.expand(s)); err != nil {
				t.Fatalf("setup: %v\n%s", err, s)
			}
		}
		err := execExpectingError(tx, `INSERT INTO human_deltas (id, decision_episode_id, unexplained)
			VALUES ('55555555-5555-4555-8555-555555555555','22222222-2222-4222-8222-222222222222',false);
			INSERT INTO human_delta_explanations (human_delta_id, eval_run_id)
			VALUES ('55555555-5555-4555-8555-555555555555','33333333-3333-4333-8333-333333333333')`)
		assertSQLState(t, err, checkViolation)
	})
}
