-- +goose Up
-- HAR-106 (WP8, ADR-0015): signals become a queryable history. Each signal records when the change
-- happened (occurred_at), the end of its EVENT window (expires_at; NULL for STANDING signals, whose
-- openness the AccountState decides, ADR-0011), the claim behind the state item it reports
-- (subject_claim_id, ADR-0011: blockers and commitments are matched by item key) and an idempotency
-- key. Trigger evaluations become idempotent per state diff: one burst, one diff, one evaluation.
-- (Numbered 0016 at merge time: main's highest + 1 with no gaps; renumber if another migration lands first.)

ALTER TABLE signals
    ADD COLUMN subject_claim_id uuid REFERENCES claims (id) ON DELETE RESTRICT,
    ADD COLUMN occurred_at      timestamptz,
    ADD COLUMN expires_at       timestamptz,
    ADD COLUMN dedupe_key       text;

UPDATE signals SET occurred_at = created_at;
UPDATE signals SET expires_at = occurred_at + interval '14 days' WHERE signal_type IN (
    'new_stakeholder_entered', 'champion_weakened', 'champion_reactivated', 'champion_delegated', 'blocker_resolved',
    'pricing_interest', 'expansion_interest', 'customer_replied', 'meeting_accepted', 'product_usage_increased',
    'stage_regressed', 'stage_advanced');

ALTER TABLE signals
    ALTER COLUMN occurred_at SET NOT NULL,
    ALTER COLUMN occurred_at SET DEFAULT now(),
    ADD CONSTRAINT signals_dedupe_key_len CHECK (dedupe_key IS NULL OR length(dedupe_key) BETWEEN 1 AND 300),
    -- signal.v1.json eventSignalType / standingSignalType: EVENT signals have a window, STANDING ones do not.
    ADD CONSTRAINT signals_window_matches_kind CHECK (
        CASE WHEN signal_type IN (
            'new_stakeholder_entered', 'champion_weakened', 'champion_reactivated', 'champion_delegated', 'blocker_resolved',
            'pricing_interest', 'expansion_interest', 'customer_replied', 'meeting_accepted', 'product_usage_increased',
            'stage_regressed', 'stage_advanced')
        THEN expires_at IS NOT NULL AND expires_at >= occurred_at
        ELSE expires_at IS NULL
        END);

CREATE UNIQUE INDEX signals_dedupe_uniq ON signals (account_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
-- "Signals open as of T for an account": occurred_at <= T and (expires_at IS NULL OR expires_at >= T).
CREATE INDEX signals_asof_idx ON signals (account_id, occurred_at DESC);
CREATE INDEX signals_subject_claim_idx ON signals (subject_claim_id) WHERE subject_claim_id IS NOT NULL;

-- One eligibility decision per diff and workflow (re-evaluating a version is a no-op).
CREATE UNIQUE INDEX trigger_evaluations_one_per_diff ON trigger_evaluations (state_diff_id, workflow)
    WHERE state_diff_id IS NOT NULL;

-- A dry run gets a terminal status of its own (ADR-0015): 'recorded' means its execute step was recorded and
-- nothing was sent. It is outside the open-run predicate, so the next material diff can start a new run.
ALTER TABLE agent_runs DROP CONSTRAINT agent_runs_status_check;
ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_status_check CHECK (status IN (
    'pending', 'context_built', 'drafted', 'awaiting_human', 'approved', 'edited',
    'rejected', 'ignored', 'executed', 'recorded', 'failed', 'cancelled'));
ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_recorded_only_dry CHECK (status <> 'recorded' OR run_mode = 'dry_run');

-- A writer that omits the window of an EVENT signal gets the default 14 days (signal.v1.json eventWindowDays);
-- the CHECK above still rejects a window before occurred_at and any window on a STANDING signal.
-- +goose StatementBegin
CREATE FUNCTION signals_default_window() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.expires_at IS NULL AND NEW.signal_type IN (
        'new_stakeholder_entered', 'champion_weakened', 'champion_reactivated', 'champion_delegated', 'blocker_resolved',
        'pricing_interest', 'expansion_interest', 'customer_replied', 'meeting_accepted', 'product_usage_increased',
        'stage_regressed', 'stage_advanced') THEN
        NEW.expires_at := NEW.occurred_at + interval '14 days';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER signals_default_window BEFORE INSERT ON signals FOR EACH ROW EXECUTE FUNCTION signals_default_window();

-- +goose Down
DROP TRIGGER signals_default_window ON signals;
DROP FUNCTION signals_default_window();
ALTER TABLE agent_runs DROP CONSTRAINT agent_runs_recorded_only_dry;
UPDATE agent_runs SET status = 'cancelled' WHERE status = 'recorded';
ALTER TABLE agent_runs DROP CONSTRAINT agent_runs_status_check;
ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_status_check CHECK (status IN (
    'pending', 'context_built', 'drafted', 'awaiting_human', 'approved', 'edited',
    'rejected', 'ignored', 'executed', 'failed', 'cancelled'));
DROP INDEX trigger_evaluations_one_per_diff;
DROP INDEX signals_subject_claim_idx;
DROP INDEX signals_asof_idx;
DROP INDEX signals_dedupe_uniq;
ALTER TABLE signals
    DROP CONSTRAINT signals_window_matches_kind,
    DROP CONSTRAINT signals_dedupe_key_len,
    DROP COLUMN dedupe_key,
    DROP COLUMN expires_at,
    DROP COLUMN occurred_at,
    DROP COLUMN subject_claim_id;
