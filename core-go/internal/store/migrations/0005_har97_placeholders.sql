-- +goose Up
-- Persistence hooks for HAR-97 (evals, reactions, outcomes, knowledge, rep profiles).
-- HAR-96 creates and wires these tables; HAR-97 defines the behavior that fills them.

CREATE TABLE eval_runs (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_run_id       uuid NOT NULL REFERENCES agent_runs (id) ON DELETE CASCADE,
    evaluator          text NOT NULL,
    evaluator_version  text NOT NULL,
    kind               text NOT NULL CHECK (kind IN ('deterministic', 'semantic', 'trace', 'human_delta')),
    verdict            text NOT NULL CHECK (verdict IN ('pass', 'fail', 'warn', 'abstain')),
    score              numeric,
    rationale          text,
    evidence_refs      jsonb NOT NULL DEFAULT '[]'::jsonb,
    draft_index        integer NOT NULL DEFAULT 1 CHECK (draft_index >= 1),
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX eval_runs_run_idx ON eval_runs (agent_run_id);

CREATE TABLE customer_reactions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id     uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    agent_run_id   uuid REFERENCES agent_runs (id) ON DELETE SET NULL,
    activity_id    uuid NOT NULL REFERENCES activities (id) ON DELETE CASCADE,
    reaction_type  text NOT NULL CHECK (reaction_type IN (
        'replied', 'ignored', 'meeting_accepted', 'stakeholder_added', 'stakeholder_removed',
        'objection_raised', 'new_commitment', 'requested_document', 'conversation_advanced',
        'conversation_cooled')),
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customer_reactions_account_idx ON customer_reactions (account_id, created_at DESC);
CREATE INDEX customer_reactions_activity_idx ON customer_reactions (activity_id);
CREATE INDEX customer_reactions_run_idx ON customer_reactions (agent_run_id) WHERE agent_run_id IS NOT NULL;

CREATE TABLE business_outcomes (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    opportunity_id  uuid REFERENCES opportunities (id) ON DELETE SET NULL,
    outcome_type    text NOT NULL CHECK (outcome_type IN (
        'stage_advanced', 'stage_regressed', 'renewal', 'expansion', 'closed_won', 'closed_lost',
        'acv_change', 'cycle_change')),
    value           jsonb NOT NULL DEFAULT '{}'::jsonb,
    activity_id     uuid REFERENCES activities (id) ON DELETE SET NULL,
    occurred_at     timestamptz NOT NULL
);

CREATE TABLE knowledge (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title                     text NOT NULL,
    situation_signature       jsonb NOT NULL DEFAULT '{}'::jsonb,
    applicability_conditions  jsonb NOT NULL DEFAULT '[]'::jsonb,
    guidance                  text NOT NULL,
    status                    text NOT NULL DEFAULT 'candidate' CHECK (status IN (
        'candidate', 'provisional', 'supported', 'confirmed', 'disputed', 'stale')),
    support_count             integer NOT NULL DEFAULT 0 CHECK (support_count >= 0),
    exceptions                jsonb NOT NULL DEFAULT '[]'::jsonb,
    provenance                jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at                timestamptz NOT NULL DEFAULT now(),
    last_validated_at         timestamptz
);

CREATE TABLE knowledge_evidence (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    knowledge_id  uuid NOT NULL REFERENCES knowledge (id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN (
        'decision_episode', 'human_decision', 'customer_reaction', 'business_outcome', 'counterexample')),
    ref_id        uuid NOT NULL,
    note          text,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX knowledge_evidence_knowledge_idx ON knowledge_evidence (knowledge_id);

CREATE TABLE rep_profiles (
    person_id   uuid PRIMARY KEY REFERENCES people (id) ON DELETE CASCADE,
    profile     jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE rep_profiles;
DROP TABLE knowledge_evidence;
DROP TABLE knowledge;
DROP TABLE business_outcomes;
DROP TABLE customer_reactions;
DROP TABLE eval_runs;
