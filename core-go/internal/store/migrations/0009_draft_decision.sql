-- +goose Up
-- The account agent's structured decision (action, why_now, used_guidance, wait_until, who to involve / not
-- involve) is stored with its draft instead of being lost (HAR-97 §14; contracts agent_run_draft.v1 decision).
ALTER TABLE agent_run_drafts
    ADD COLUMN decision jsonb CHECK (decision IS NULL OR jsonb_typeof(decision) = 'object');

-- +goose Down
ALTER TABLE agent_run_drafts DROP COLUMN decision;
