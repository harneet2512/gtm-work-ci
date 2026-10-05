-- +goose Up
-- HAR-97: drafts, structured eval results, versioned evaluators, decision guidance,
-- decision episodes, human deltas, knowledge lifecycle, and the eval review set.
-- Supervision records (runs, drafts, evals, decisions, episodes, deltas) are never removed by
-- cascade: deletes are RESTRICTed and retention must be an explicit purge job.

-- One vocabulary of evaluator types, shared by eval_runs and evaluator_versions.
CREATE DOMAIN eval_type AS text CHECK (VALUE IN (
    'recipient_correctness', 'date_commitment_consistency', 'pricing_integrity', 'crm_writeback',
    'duplicate_action', 'provenance_coverage', 'permission_policy',
    'buyer_readiness', 'cta_calibration', 'next_step_quality', 'stakeholder_selection',
    'stakeholder_coverage', 'economic_buyer_coverage', 'champion_strength', 'champion_continuity',
    'decision_process', 'business_case', 'momentum', 'action_stage_fit', 'expansion_readiness',
    'customer_risk_sensitivity', 'relationship_pressure', 'timing_cadence', 'next_action_quality',
    'grounding', 'commitment_consistency', 'state_change_relevance', 'channel_appropriateness',
    'rep_style', 'knowledge_applicability', 'exception_awareness', 'evidence_sufficiency',
    'trajectory', 'human_delta'));

-- Audit chain: runs keep their decisions, evals and context log.
ALTER TABLE human_decisions DROP CONSTRAINT human_decisions_agent_run_id_fkey,
    ADD CONSTRAINT human_decisions_agent_run_id_fkey FOREIGN KEY (agent_run_id)
        REFERENCES agent_runs (id) ON DELETE RESTRICT;
ALTER TABLE eval_runs DROP CONSTRAINT eval_runs_agent_run_id_fkey,
    ADD CONSTRAINT eval_runs_agent_run_id_fkey FOREIGN KEY (agent_run_id)
        REFERENCES agent_runs (id) ON DELETE RESTRICT;
ALTER TABLE context_access_log DROP CONSTRAINT context_access_log_agent_run_id_fkey,
    ADD CONSTRAINT context_access_log_agent_run_id_fkey FOREIGN KEY (agent_run_id)
        REFERENCES agent_runs (id) ON DELETE RESTRICT;
ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_id_account_uniq UNIQUE (id, account_id);

-- Draft history: Draft 1 (account agent) -> evals -> Draft 2..n (revision planner).
-- agent_runs.output mirrors the latest draft for convenience; drafts are the source of truth.
CREATE TABLE agent_run_drafts (
    agent_run_id       uuid NOT NULL REFERENCES agent_runs (id) ON DELETE RESTRICT,
    draft_index        integer NOT NULL CHECK (draft_index >= 1),
    source             text NOT NULL CHECK (source IN ('account_agent', 'revision_planner')),
    output             jsonb NOT NULL,
    revision_feedback  jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(revision_feedback) = 'array'),
    model              text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_run_id, draft_index),
    CONSTRAINT agent_run_drafts_first_from_agent CHECK ((draft_index = 1) = (source = 'account_agent'))
);
-- Backfill Draft 1 for runs that already have an output (no-op on a fresh database).
INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output)
SELECT id, 1, 'account_agent', output FROM agent_runs WHERE output IS NOT NULL;

-- Versioned evaluators; exactly one active version per evaluator, changed only through
-- promote_evaluator_version() and the transition trigger below.
CREATE TABLE evaluator_versions (
    evaluator              eval_type NOT NULL,
    version                integer NOT NULL CHECK (version >= 1),
    status                 text NOT NULL CHECK (status IN ('candidate', 'shadow', 'active', 'retired')),
    kind                   text NOT NULL CHECK (kind IN ('deterministic', 'semantic', 'trace', 'human_delta')),
    rubric                 text NOT NULL CHECK (length(rubric) BETWEEN 1 AND 8000),
    labels                 text[] NOT NULL DEFAULT '{}',
    examples               jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(examples) = 'array'),
    created_from           text NOT NULL CHECK (created_from IN ('seed', 'human_delta', 'manual')),
    source_human_delta_id  uuid,  -- FK added below
    metrics                jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metrics) = 'object'),
    created_at             timestamptz NOT NULL DEFAULT now(),
    promoted_at            timestamptz,
    PRIMARY KEY (evaluator, version),
    CONSTRAINT evaluator_versions_active_promoted CHECK (status <> 'active' OR promoted_at IS NOT NULL)
);
CREATE UNIQUE INDEX evaluator_versions_one_active ON evaluator_versions (evaluator) WHERE status = 'active';
CREATE INDEX evaluator_versions_source_delta_idx ON evaluator_versions (source_human_delta_id)
    WHERE source_human_delta_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION evaluator_versions_check_transition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;
    IF (OLD.status, NEW.status) NOT IN (('candidate', 'shadow'), ('candidate', 'retired'),
                                        ('shadow', 'active'), ('shadow', 'retired'), ('active', 'retired')) THEN
        RAISE EXCEPTION 'illegal evaluator status transition % -> %', OLD.status, NEW.status
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER evaluator_versions_transition BEFORE UPDATE OF status ON evaluator_versions
    FOR EACH ROW EXECUTE FUNCTION evaluator_versions_check_transition();

-- Atomic promotion: lock the evaluator's versions, retire the active one, activate the shadow.
-- +goose StatementBegin
CREATE FUNCTION promote_evaluator_version(p_evaluator text, p_version integer) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM 1 FROM evaluator_versions WHERE evaluator = p_evaluator FOR UPDATE;
    IF NOT EXISTS (SELECT 1 FROM evaluator_versions
                   WHERE evaluator = p_evaluator AND version = p_version AND status = 'shadow') THEN
        RAISE EXCEPTION 'evaluator %:v% is not in shadow', p_evaluator, p_version USING ERRCODE = 'check_violation';
    END IF;
    UPDATE evaluator_versions SET status = 'retired' WHERE evaluator = p_evaluator AND status = 'active';
    UPDATE evaluator_versions SET status = 'active', promoted_at = now()
     WHERE evaluator = p_evaluator AND version = p_version;
END $$;
-- +goose StatementEnd

-- Structured EvalResult columns on eval_runs (table created in 0005). Contract mapping:
-- eval_type=evaluator, eval_version=evaluator_version, reason=rationale.
ALTER TABLE eval_runs ALTER COLUMN evaluator TYPE eval_type;
ALTER TABLE eval_runs
    ADD COLUMN label                 text CHECK (label ~ '^[A-Z][A-Z_]*$'),
    ADD COLUMN diagnostics           text[] NOT NULL DEFAULT '{}',
    ADD COLUMN blocking              boolean NOT NULL DEFAULT false,
    ADD COLUMN state_refs            text[] NOT NULL DEFAULT '{}',
    ADD COLUMN activity_refs         uuid[] NOT NULL DEFAULT '{}',
    ADD COLUMN knowledge_refs        uuid[] NOT NULL DEFAULT '{}',
    ADD COLUMN suggested_correction  text,
    ADD COLUMN confidence            numeric(4, 3) CHECK (confidence BETWEEN 0 AND 1),
    ADD COLUMN evidence_class        text NOT NULL DEFAULT 'product_rule'
        CHECK (evidence_class IN ('deal_data', 'methodology', 'cs_ops', 'product_rule')),
    ADD COLUMN model                 text,
    ADD CONSTRAINT eval_runs_blocking_is_fail CHECK (NOT blocking OR verdict = 'fail'),
    ADD CONSTRAINT eval_runs_deterministic_no_model CHECK (kind <> 'deterministic' OR model IS NULL),
    ADD CONSTRAINT eval_runs_version_format CHECK (evaluator_version ~ '^[a-z_]+:v[0-9]+$'),
    ADD CONSTRAINT eval_runs_version_matches_type CHECK (split_part(evaluator_version, ':', 1) = evaluator::text),
    ADD CONSTRAINT eval_runs_draft_exists FOREIGN KEY (agent_run_id, draft_index)
        REFERENCES agent_run_drafts (agent_run_id, draft_index) ON DELETE RESTRICT;
-- Evidence class must be stated explicitly from now on.
ALTER TABLE eval_runs ALTER COLUMN evidence_class DROP DEFAULT;
DROP INDEX eval_runs_run_idx;
CREATE INDEX eval_runs_run_draft_idx ON eval_runs (agent_run_id, draft_index);
CREATE INDEX eval_runs_evaluator_version_idx ON eval_runs (evaluator, evaluator_version);

-- Knowledge converted into a recommendation for one account at one state version.
CREATE TABLE decision_guidance (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id     uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    agent_run_id   uuid REFERENCES agent_runs (id) ON DELETE SET NULL,
    state_version  integer NOT NULL CHECK (state_version >= 0),
    guidance       jsonb NOT NULL CHECK (jsonb_typeof(guidance) = 'object'),
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX decision_guidance_account_idx ON decision_guidance (account_id, created_at DESC);
CREATE INDEX decision_guidance_run_idx ON decision_guidance (agent_run_id) WHERE agent_run_id IS NOT NULL;

ALTER TABLE agent_runs ADD COLUMN decision_guidance_id uuid REFERENCES decision_guidance (id) ON DELETE SET NULL;
CREATE INDEX agent_runs_decision_guidance_idx ON agent_runs (decision_guidance_id) WHERE decision_guidance_id IS NOT NULL;

-- The supervised record of one decision (same account as its run; never cascaded away).
CREATE TABLE decision_episodes (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_run_id          uuid NOT NULL UNIQUE,
    account_id            uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    state_version         integer NOT NULL CHECK (state_version >= 0),
    state_diff_id         uuid REFERENCES state_diffs (id) ON DELETE RESTRICT,
    decision_guidance_id  uuid REFERENCES decision_guidance (id) ON DELETE RESTRICT,
    final_draft_index     integer NOT NULL CHECK (final_draft_index >= 1),
    human_decision_id     uuid NOT NULL UNIQUE REFERENCES human_decisions (id) ON DELETE RESTRICT,
    human_action          text NOT NULL CHECK (human_action IN (
        'APPROVE_UNCHANGED', 'APPROVE_WITH_EDIT', 'REJECT', 'IGNORE', 'MANUAL_REPLACEMENT')),
    human_final_artifact  jsonb,
    created_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT decision_episodes_run_same_account FOREIGN KEY (agent_run_id, account_id)
        REFERENCES agent_runs (id, account_id) ON DELETE RESTRICT,
    CONSTRAINT decision_episodes_edit_has_artifact
        CHECK (human_action NOT IN ('APPROVE_WITH_EDIT', 'MANUAL_REPLACEMENT') OR human_final_artifact IS NOT NULL),
    CONSTRAINT decision_episodes_final_draft_exists FOREIGN KEY (agent_run_id, final_draft_index)
        REFERENCES agent_run_drafts (agent_run_id, draft_index) ON DELETE RESTRICT
);
CREATE INDEX decision_episodes_account_idx ON decision_episodes (account_id, created_at DESC);
CREATE INDEX decision_episodes_state_diff_idx ON decision_episodes (state_diff_id) WHERE state_diff_id IS NOT NULL;
CREATE INDEX decision_episodes_guidance_idx ON decision_episodes (decision_guidance_id) WHERE decision_guidance_id IS NOT NULL;

-- The episode's human_action must agree with the recorded human decision.
-- +goose StatementBegin
CREATE FUNCTION decision_episodes_check_action() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    d text;
    r uuid;
BEGIN
    SELECT decision, agent_run_id INTO d, r FROM human_decisions WHERE id = NEW.human_decision_id;
    IF r IS DISTINCT FROM NEW.agent_run_id THEN
        RAISE EXCEPTION 'human decision belongs to a different run' USING ERRCODE = 'check_violation';
    END IF;
    IF NOT ((d = 'approve' AND NEW.human_action = 'APPROVE_UNCHANGED')
         OR (d = 'edit' AND NEW.human_action IN ('APPROVE_WITH_EDIT', 'MANUAL_REPLACEMENT'))
         OR (d = 'reject' AND NEW.human_action = 'REJECT')
         OR (d = 'ignore' AND NEW.human_action = 'IGNORE')) THEN
        RAISE EXCEPTION 'human_action % does not match decision %', NEW.human_action, d
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER decision_episodes_action_matches BEFORE INSERT OR UPDATE ON decision_episodes
    FOR EACH ROW EXECUTE FUNCTION decision_episodes_check_action();

CREATE TABLE human_deltas (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    decision_episode_id  uuid NOT NULL UNIQUE REFERENCES decision_episodes (id) ON DELETE RESTRICT,
    literal_changes      jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(literal_changes) = 'array'),
    semantic_labels      text[] NOT NULL DEFAULT '{}',
    unexplained          boolean NOT NULL,
    candidate_criterion  jsonb,
    model                text,
    created_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT human_deltas_unexplained_has_candidate CHECK (NOT unexplained OR candidate_criterion IS NOT NULL)
);
CREATE INDEX human_deltas_unexplained_idx ON human_deltas (created_at DESC) WHERE unexplained;

-- Which eval results predicted a human change (replaces an unchecked uuid[]).
CREATE TABLE human_delta_explanations (
    human_delta_id  uuid NOT NULL REFERENCES human_deltas (id) ON DELETE RESTRICT,
    eval_run_id     uuid NOT NULL REFERENCES eval_runs (id) ON DELETE RESTRICT,
    PRIMARY KEY (human_delta_id, eval_run_id)
);
CREATE INDEX human_delta_explanations_eval_idx ON human_delta_explanations (eval_run_id);

-- Explanations must come from the same run; explained deltas need >=1, unexplained none.
-- Checked at commit so a delta and its explanations can be inserted in either order.
-- +goose StatementBegin
CREATE FUNCTION human_deltas_check_explanations() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    delta_id uuid;
    is_unexplained boolean;
    n integer;
    foreign_evals integer;
BEGIN
    -- IF, not CASE: a CASE expression would resolve both NEW fields and fail on one table.
    IF TG_TABLE_NAME = 'human_deltas' THEN
        delta_id := NEW.id;
    ELSE
        delta_id := NEW.human_delta_id;
    END IF;
    SELECT hd.unexplained INTO is_unexplained FROM human_deltas hd WHERE hd.id = delta_id;
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;
    SELECT count(*),
           count(*) FILTER (WHERE er.agent_run_id IS DISTINCT FROM de.agent_run_id)
      INTO n, foreign_evals
      FROM human_delta_explanations x
      JOIN human_deltas hd ON hd.id = x.human_delta_id
      JOIN decision_episodes de ON de.id = hd.decision_episode_id
      JOIN eval_runs er ON er.id = x.eval_run_id
     WHERE x.human_delta_id = delta_id;
    IF foreign_evals > 0 THEN
        RAISE EXCEPTION 'explanation eval belongs to another run' USING ERRCODE = 'check_violation';
    END IF;
    IF is_unexplained AND n > 0 THEN
        RAISE EXCEPTION 'unexplained delta cannot cite explaining evals' USING ERRCODE = 'check_violation';
    END IF;
    IF NOT is_unexplained AND n = 0 THEN
        RAISE EXCEPTION 'explained delta must cite at least one eval' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER human_deltas_explanations_check AFTER INSERT OR UPDATE ON human_deltas
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION human_deltas_check_explanations();
CREATE CONSTRAINT TRIGGER human_delta_explanations_check AFTER INSERT OR UPDATE ON human_delta_explanations
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION human_deltas_check_explanations();

ALTER TABLE evaluator_versions ADD CONSTRAINT evaluator_versions_source_delta_fk
    FOREIGN KEY (source_human_delta_id) REFERENCES human_deltas (id) ON DELETE RESTRICT;

-- Knowledge: structured guidance, counts, lifecycle history. The contract's
-- supporting_decision_episode_ids are derived from knowledge_evidence (kind = decision_episode);
-- guidance {summary, do, dont} maps to guidance / guidance_do / guidance_dont.
CREATE SEQUENCE knowledge_key_seq START 1;
UPDATE knowledge SET situation_signature = '[]'::jsonb WHERE jsonb_typeof(situation_signature) <> 'array';
ALTER TABLE knowledge
    ADD COLUMN key                 text NOT NULL UNIQUE DEFAULT ('K' || nextval('knowledge_key_seq')) CHECK (key ~ '^K[0-9]+$'),
    ADD COLUMN guidance_do         jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(guidance_do) = 'array'),
    ADD COLUMN guidance_dont       jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(guidance_dont) = 'array'),
    ADD COLUMN counts              jsonb NOT NULL DEFAULT
        '{"decisions": 0, "positive_reactions": 0, "negative_reactions": 0, "outcomes_advanced": 0, "counterexamples": 0}'::jsonb
        CHECK (counts ?& ARRAY['decisions', 'positive_reactions', 'negative_reactions', 'outcomes_advanced', 'counterexamples']),
    ADD COLUMN counterexamples     jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(counterexamples) = 'array'),
    ADD COLUMN evidence_classes    text[] NOT NULL DEFAULT '{}',
    ADD COLUMN used_by_evaluators  text[] NOT NULL DEFAULT '{}'
        CHECK (array_to_string(used_by_evaluators, ',') ~ '^([a-z_]+:v[0-9]+(,|$))*$'),
    ALTER COLUMN situation_signature SET DEFAULT '[]'::jsonb,
    ADD CONSTRAINT knowledge_signature_is_array CHECK (jsonb_typeof(situation_signature) = 'array'),
    ADD CONSTRAINT knowledge_exceptions_is_array CHECK (jsonb_typeof(exceptions) = 'array');
-- Applicability lookups only consider knowledge that may be applied.
CREATE INDEX knowledge_applicable_idx ON knowledge (status) WHERE status IN ('provisional', 'supported', 'confirmed');

ALTER TABLE knowledge_evidence ADD CONSTRAINT knowledge_evidence_once UNIQUE (knowledge_id, kind, ref_id);
CREATE INDEX knowledge_evidence_ref_idx ON knowledge_evidence (kind, ref_id);

CREATE TABLE knowledge_status_history (
    id               bigserial PRIMARY KEY,
    knowledge_id     uuid NOT NULL REFERENCES knowledge (id) ON DELETE RESTRICT,
    from_status      text CHECK (from_status IN ('candidate', 'provisional', 'supported', 'confirmed', 'disputed', 'stale')),
    to_status        text NOT NULL CHECK (to_status IN ('candidate', 'provisional', 'supported', 'confirmed', 'disputed', 'stale')),
    reason           text NOT NULL,
    evidence_kind    text CHECK (evidence_kind IN ('decision_episode', 'human_decision', 'customer_reaction', 'business_outcome', 'counterexample')),
    evidence_ref_id  uuid,
    changed_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_status_history_changes CHECK (from_status IS DISTINCT FROM to_status)
);
CREATE INDEX knowledge_status_history_knowledge_idx ON knowledge_status_history (knowledge_id, changed_at DESC);

-- Every status change is recorded, even if the writer forgot to add a reason row.
-- Writers that want a reason set it with: SET LOCAL ghost.knowledge_reason = '...'.
-- +goose StatementBegin
CREATE FUNCTION knowledge_record_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.status IS DISTINCT FROM OLD.status THEN
        INSERT INTO knowledge_status_history (knowledge_id, from_status, to_status, reason)
        VALUES (NEW.id, CASE WHEN TG_OP = 'INSERT' THEN NULL ELSE OLD.status END, NEW.status,
                coalesce(nullif(current_setting('ghost.knowledge_reason', true), ''), 'unspecified'));
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER knowledge_status_history_trg AFTER INSERT OR UPDATE OF status ON knowledge
    FOR EACH ROW EXECUTE FUNCTION knowledge_record_status();

-- Customer reactions and business outcomes attribute to decision episodes; reactions dedupe.
ALTER TABLE customer_reactions
    ADD COLUMN polarity             text NOT NULL DEFAULT 'neutral' CHECK (polarity IN ('positive', 'neutral', 'negative')),
    ADD COLUMN evidence_refs        jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    ADD COLUMN decision_episode_id  uuid REFERENCES decision_episodes (id) ON DELETE RESTRICT,
    ADD CONSTRAINT customer_reactions_once UNIQUE (activity_id, reaction_type);
CREATE INDEX customer_reactions_episode_idx ON customer_reactions (decision_episode_id) WHERE decision_episode_id IS NOT NULL;
ALTER TABLE business_outcomes
    ADD COLUMN decision_episode_id  uuid REFERENCES decision_episodes (id) ON DELETE RESTRICT;
CREATE INDEX business_outcomes_episode_idx ON business_outcomes (decision_episode_id) WHERE decision_episode_id IS NOT NULL;

-- Offline benchmark cases and the online disagreement review set (HAR-97 §20-21).
CREATE TABLE eval_cases (
    id          text PRIMARY KEY CHECK (id ~ '^[a-z0-9_]+$'),
    case_type   text NOT NULL CHECK (case_type ~ '^[a-z_]+$'),
    source      text NOT NULL CHECK (source IN ('offline', 'online_disagreement')),
    payload     jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX eval_cases_source_type_idx ON eval_cases (source, case_type);

-- +goose Down
DROP TABLE eval_cases;
DROP INDEX business_outcomes_episode_idx;
ALTER TABLE business_outcomes DROP COLUMN decision_episode_id;
DROP INDEX customer_reactions_episode_idx;
ALTER TABLE customer_reactions DROP CONSTRAINT customer_reactions_once,
    DROP COLUMN decision_episode_id, DROP COLUMN evidence_refs, DROP COLUMN polarity;
DROP TRIGGER knowledge_status_history_trg ON knowledge;
DROP FUNCTION knowledge_record_status();
DROP TABLE knowledge_status_history;
DROP INDEX knowledge_evidence_ref_idx;
ALTER TABLE knowledge_evidence DROP CONSTRAINT knowledge_evidence_once;
DROP INDEX knowledge_applicable_idx;
ALTER TABLE knowledge
    DROP CONSTRAINT knowledge_exceptions_is_array, DROP CONSTRAINT knowledge_signature_is_array,
    ALTER COLUMN situation_signature SET DEFAULT '{}'::jsonb,
    DROP COLUMN used_by_evaluators, DROP COLUMN evidence_classes, DROP COLUMN counterexamples,
    DROP COLUMN counts, DROP COLUMN guidance_dont, DROP COLUMN guidance_do, DROP COLUMN key;
DROP SEQUENCE knowledge_key_seq;
ALTER TABLE evaluator_versions DROP CONSTRAINT evaluator_versions_source_delta_fk;
DROP TRIGGER human_delta_explanations_check ON human_delta_explanations;
DROP TRIGGER human_deltas_explanations_check ON human_deltas;
DROP FUNCTION human_deltas_check_explanations();
DROP TABLE human_delta_explanations;
DROP TABLE human_deltas;
DROP TRIGGER decision_episodes_action_matches ON decision_episodes;
DROP FUNCTION decision_episodes_check_action();
DROP TABLE decision_episodes;
DROP INDEX agent_runs_decision_guidance_idx;
ALTER TABLE agent_runs DROP COLUMN decision_guidance_id;
DROP TABLE decision_guidance;
DROP INDEX eval_runs_evaluator_version_idx;
DROP INDEX eval_runs_run_draft_idx;
CREATE INDEX eval_runs_run_idx ON eval_runs (agent_run_id);
ALTER TABLE eval_runs
    DROP CONSTRAINT eval_runs_draft_exists, DROP CONSTRAINT eval_runs_version_matches_type,
    DROP CONSTRAINT eval_runs_version_format, DROP CONSTRAINT eval_runs_deterministic_no_model,
    DROP CONSTRAINT eval_runs_blocking_is_fail,
    DROP COLUMN model, DROP COLUMN evidence_class, DROP COLUMN confidence, DROP COLUMN suggested_correction,
    DROP COLUMN knowledge_refs, DROP COLUMN activity_refs, DROP COLUMN state_refs, DROP COLUMN blocking,
    DROP COLUMN diagnostics, DROP COLUMN label;
ALTER TABLE eval_runs ALTER COLUMN evaluator TYPE text;
DROP FUNCTION promote_evaluator_version(text, integer);
DROP TRIGGER evaluator_versions_transition ON evaluator_versions;
DROP FUNCTION evaluator_versions_check_transition();
DROP TABLE evaluator_versions;
DROP TABLE agent_run_drafts;
ALTER TABLE agent_runs DROP CONSTRAINT agent_runs_id_account_uniq;
ALTER TABLE context_access_log DROP CONSTRAINT context_access_log_agent_run_id_fkey,
    ADD CONSTRAINT context_access_log_agent_run_id_fkey FOREIGN KEY (agent_run_id)
        REFERENCES agent_runs (id) ON DELETE CASCADE;
ALTER TABLE eval_runs DROP CONSTRAINT eval_runs_agent_run_id_fkey,
    ADD CONSTRAINT eval_runs_agent_run_id_fkey FOREIGN KEY (agent_run_id)
        REFERENCES agent_runs (id) ON DELETE CASCADE;
ALTER TABLE human_decisions DROP CONSTRAINT human_decisions_agent_run_id_fkey,
    ADD CONSTRAINT human_decisions_agent_run_id_fkey FOREIGN KEY (agent_run_id)
        REFERENCES agent_runs (id) ON DELETE CASCADE;
DROP DOMAIN eval_type;
