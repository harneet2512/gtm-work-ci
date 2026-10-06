-- +goose Up
-- HAR-120 (reactions + outcomes as supervision): the 0005 placeholder left business_outcomes without
-- the run link and evidence refs customer_reactions already carries. This adds both, plus the same
-- (activity, type) dedupe customer_reactions_once gives reactions, so a re-scan writes nothing twice.
-- business_outcomes.activity_id is nullable, so a NULL-keyed row never collides — the detector always
-- sets it, because an outcome is only as good as the activity that proves it.
ALTER TABLE business_outcomes
    ADD COLUMN agent_run_id  uuid REFERENCES agent_runs (id) ON DELETE SET NULL,
    ADD COLUMN evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    ADD CONSTRAINT business_outcomes_once UNIQUE (activity_id, outcome_type);
CREATE INDEX business_outcomes_run_idx ON business_outcomes (agent_run_id) WHERE agent_run_id IS NOT NULL;

-- A silence episode emits one 'ignored' reaction per decision episode, not one per daily tick:
-- sustained silence is one negative signal, not N (HAR-120).
CREATE UNIQUE INDEX customer_reactions_ignored_once ON customer_reactions (decision_episode_id)
    WHERE reaction_type = 'ignored';

-- +goose Down
DROP INDEX customer_reactions_ignored_once;
DROP INDEX business_outcomes_run_idx;
ALTER TABLE business_outcomes
    DROP CONSTRAINT business_outcomes_once, DROP COLUMN evidence_refs, DROP COLUMN agent_run_id;
