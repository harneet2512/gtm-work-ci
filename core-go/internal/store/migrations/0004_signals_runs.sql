-- +goose Up
-- Signals, trigger evaluations, agent runs (typed steps), server-side context access log
-- and human decisions.

CREATE TABLE signals (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    opportunity_id     uuid,
    signal_type        text NOT NULL CHECK (signal_type IN (
        'new_stakeholder_entered', 'champion_weakened', 'champion_reactivated', 'champion_delegated',
        'security_blocker_appeared', 'blocker_resolved', 'pricing_interest', 'expansion_interest',
        'stakeholder_gap', 'next_meeting_missing', 'commitment_overdue', 'customer_replied',
        'customer_went_silent', 'meeting_accepted', 'support_risk_spike', 'product_usage_increased',
        'stage_regressed', 'stage_advanced')),
    state_diff_id      uuid REFERENCES state_diffs (id) ON DELETE RESTRICT,
    subject_person_id  uuid REFERENCES people (id) ON DELETE SET NULL,
    rule               text NOT NULL,
    details            jsonb NOT NULL DEFAULT '{}'::jsonb,
    evidence_refs      jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT signals_opportunity_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT
);
CREATE INDEX signals_account_time_idx ON signals (account_id, created_at DESC);
CREATE INDEX signals_diff_idx ON signals (state_diff_id);
CREATE INDEX signals_subject_idx ON signals (subject_person_id) WHERE subject_person_id IS NOT NULL;

-- Eligibility decisions, persisted for eligible AND ineligible outcomes. The link to a run
-- lives only on agent_runs (one direction, no FK cycle).
CREATE TABLE trigger_evaluations (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id     uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    workflow       text NOT NULL CHECK (workflow IN ('post_interaction_followup')),
    eligible       boolean NOT NULL,
    reason_codes   text[] NOT NULL,
    explanation    text,
    signal_ids     uuid[] NOT NULL DEFAULT '{}',
    state_diff_id  uuid REFERENCES state_diffs (id) ON DELETE RESTRICT,
    evaluated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT trigger_evaluations_has_reason CHECK (cardinality(reason_codes) >= 1),
    CONSTRAINT trigger_evaluations_known_reasons CHECK (reason_codes <@ ARRAY[
        'eligible_customer_replied', 'eligible_meeting_completed', 'eligible_stakeholder_change',
        'eligible_blocker_change', 'no_material_change', 'no_relevant_signal', 'open_run_exists',
        'rep_already_replied', 'cooldown', 'champion_unknown', 'permission_denied', 'account_unresolved']::text[]),
    -- Eligible evaluations carry only eligible_* reasons; ineligible ones carry none.
    CONSTRAINT trigger_evaluations_reasons_match_outcome CHECK (
        CASE WHEN eligible
            THEN reason_codes <@ ARRAY['eligible_customer_replied', 'eligible_meeting_completed',
                                       'eligible_stakeholder_change', 'eligible_blocker_change']::text[]
            ELSE NOT (reason_codes && ARRAY['eligible_customer_replied', 'eligible_meeting_completed',
                                            'eligible_stakeholder_change', 'eligible_blocker_change']::text[])
        END),
    -- Target for the composite FK that only lets ELIGIBLE evaluations spawn runs.
    CONSTRAINT trigger_evaluations_id_account_eligible_uniq UNIQUE (id, account_id, eligible)
);
CREATE INDEX trigger_evaluations_account_time_idx ON trigger_evaluations (account_id, evaluated_at DESC);
CREATE INDEX trigger_evaluations_diff_idx ON trigger_evaluations (state_diff_id);

CREATE TABLE agent_runs (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id             uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    opportunity_id         uuid,
    workflow               text NOT NULL CHECK (workflow IN ('post_interaction_followup')),
    run_mode               text NOT NULL CHECK (run_mode IN ('dry_run', 'live')),
    status                 text NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending', 'context_built', 'drafted', 'awaiting_human', 'approved', 'edited',
        'rejected', 'ignored', 'executed', 'failed', 'cancelled')),
    trigger_evaluation_id  uuid NOT NULL UNIQUE,
    trigger_eligible       boolean NOT NULL DEFAULT true CHECK (trigger_eligible),
    trigger_activity_ids   uuid[] NOT NULL,
    correlation_id         uuid,
    state_version          integer,
    output                 jsonb,
    knowledge_refs_used    jsonb NOT NULL DEFAULT '[]'::jsonb,  -- populated by HAR-97
    model                  text,
    error                  text,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT agent_runs_has_trigger_activities CHECK (cardinality(trigger_activity_ids) >= 1),
    -- A dry run can never reach the executed state.
    CONSTRAINT agent_runs_dry_run_never_executes CHECK (run_mode = 'live' OR status <> 'executed'),
    CONSTRAINT agent_runs_from_eligible_evaluation FOREIGN KEY (trigger_evaluation_id, account_id, trigger_eligible)
        REFERENCES trigger_evaluations (id, account_id, eligible) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_opportunity_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT,
    -- Target for the steps' composite FK so a step cannot claim a different run_mode.
    CONSTRAINT agent_runs_id_mode_uniq UNIQUE (id, run_mode)
);
CREATE INDEX agent_runs_account_time_idx ON agent_runs (account_id, created_at DESC);
CREATE INDEX agent_runs_opportunity_idx ON agent_runs (opportunity_id) WHERE opportunity_id IS NOT NULL;
CREATE INDEX agent_runs_trigger_activity_ids_gin ON agent_runs USING gin (trigger_activity_ids);
-- At most one unfinished run per account+workflow ("open_run_exists").
CREATE UNIQUE INDEX agent_runs_one_open_uniq ON agent_runs (account_id, workflow)
    WHERE status IN ('pending', 'context_built', 'drafted', 'awaiting_human', 'approved', 'edited');

CREATE TABLE agent_run_steps (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_run_id        uuid NOT NULL,
    seq                 integer NOT NULL CHECK (seq >= 1),
    step                text NOT NULL CHECK (step IN ('build_context', 'draft', 'crm_intent', 'await_human', 'execute')),
    run_mode            text NOT NULL CHECK (run_mode IN ('dry_run', 'live')),
    status              text NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped', 'recorded')),
    started_at          timestamptz,
    finished_at         timestamptz,
    detail              jsonb NOT NULL DEFAULT '{}'::jsonb,
    external_effect_id  text,
    CONSTRAINT agent_run_steps_seq_uniq UNIQUE (agent_run_id, seq),
    -- run_mode is inherited from the run, never chosen per step.
    CONSTRAINT agent_run_steps_mode_matches_run FOREIGN KEY (agent_run_id, run_mode)
        REFERENCES agent_runs (id, run_mode) ON DELETE CASCADE,
    -- Structural dry-run guarantee.
    CONSTRAINT agent_run_steps_dry_run_no_effect CHECK (run_mode = 'live' OR external_effect_id IS NULL),
    CONSTRAINT agent_run_steps_dry_run_execute_only_recorded
        CHECK (step <> 'execute' OR run_mode = 'live' OR status IN ('recorded', 'skipped', 'pending')),
    CONSTRAINT agent_run_steps_dry_run_no_effect_detail CHECK (run_mode = 'live' OR NOT (detail ? 'external_effect'))
);

-- Server-side record of every context pull. AgentRun.input_context_refs is derived from here,
-- so the agent cannot claim context it never read.
CREATE TABLE context_access_log (
    id            bigserial PRIMARY KEY,
    agent_run_id  uuid NOT NULL REFERENCES agent_runs (id) ON DELETE CASCADE,
    tool          text NOT NULL CHECK (tool IN ('state', 'recent_diffs', 'evidence', 'activities', 'people', 'commitments')),
    args          jsonb NOT NULL DEFAULT '{}'::jsonb,
    returned_ids  jsonb NOT NULL DEFAULT '[]'::jsonb,
    bytes         integer NOT NULL CHECK (bytes >= 0),
    truncated     boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX context_access_log_run_idx ON context_access_log (agent_run_id, id);

-- One decision per run; the same object for web, Slack and MCP. A second submission
-- (e.g. Slack double-click) hits the unique constraint and is reported as 409.
CREATE TABLE human_decisions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_run_id     uuid NOT NULL UNIQUE REFERENCES agent_runs (id) ON DELETE CASCADE,
    decision         text NOT NULL CHECK (decision IN ('approve', 'edit', 'reject', 'ignore')),
    surface          text NOT NULL CHECK (surface IN ('web', 'slack', 'mcp', 'api')),
    actor_person_id  uuid REFERENCES people (id) ON DELETE SET NULL,
    actor_label      text NOT NULL CHECK (length(actor_label) BETWEEN 1 AND 200),
    edited_artifact  jsonb,
    reason           text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT human_decisions_edit_has_artifact CHECK (decision <> 'edit' OR edited_artifact IS NOT NULL)
);
CREATE INDEX human_decisions_actor_idx ON human_decisions (actor_person_id) WHERE actor_person_id IS NOT NULL;

-- +goose Down
DROP TABLE human_decisions;
DROP TABLE context_access_log;
DROP TABLE agent_run_steps;
DROP TABLE agent_runs;
DROP TABLE trigger_evaluations;
DROP TABLE signals;
