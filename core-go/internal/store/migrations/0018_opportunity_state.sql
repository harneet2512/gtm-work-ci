-- +goose Up
-- ADR-0016: per-opportunity state. Claims already carry opportunity_id; the reducer now folds them per deal.
-- Separate tables rather than a scope column on account_state/state_history (see the ADR): account_state keeps
-- one row and one version counter per account, and every reader, FK and state_diffs row of it stays untouched.
-- (Numbered 0018 at merge time: a migration's number is fixed only at merge time, as main's highest + 1 with no gaps.)

-- 'amount' (the deal's value) is a deal-scoped claim field; it folds into OpportunityState only.
ALTER TABLE claims DROP CONSTRAINT claims_field_path_check;
ALTER TABLE claims ADD CONSTRAINT claims_field_path_check CHECK (field_path IN (
    'stage', 'health', 'owner', 'motion',
    'champion', 'champion_status', 'economic_buyer', 'buying_group.member', 'stakeholder_role',
    'blockers', 'objections', 'decision_criteria', 'decision_process',
    'commitment', 'next_milestone', 'next_meeting', 'relationship_risk',
    'product_use_case', 'commercial_issue', 'delegation', 'summary', 'amount'));

-- Current projection, one row per opportunity. Writers use optimistic locking: UPDATE ... WHERE version = $expected.
CREATE TABLE opportunity_state (
    opportunity_id    uuid PRIMARY KEY,
    account_id        uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    version           integer NOT NULL CHECK (version >= 1),
    as_of             timestamptz NOT NULL,
    computed_at       timestamptz NOT NULL DEFAULT now(),
    last_activity_id  uuid REFERENCES activities (id) ON DELETE SET NULL,
    is_open           boolean NOT NULL,
    state             jsonb NOT NULL,
    CONSTRAINT opportunity_state_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT
);
CREATE INDEX opportunity_state_account_idx ON opportunity_state (account_id, is_open, as_of DESC);

-- Every version ever written, for StateAt(account, opportunity, t). A version is written only when the content changed.
CREATE TABLE opportunity_state_history (
    id                    bigserial PRIMARY KEY,
    opportunity_id        uuid NOT NULL,
    account_id            uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    version               integer NOT NULL CHECK (version >= 1),
    as_of                 timestamptz NOT NULL,
    computed_at           timestamptz NOT NULL DEFAULT now(),
    trigger_activity_ids  uuid[] NOT NULL DEFAULT '{}',
    state                 jsonb NOT NULL,
    CONSTRAINT opportunity_state_history_version_uniq UNIQUE (opportunity_id, version),
    CONSTRAINT opportunity_state_history_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT
);
CREATE INDEX opportunity_state_history_as_of_idx ON opportunity_state_history (opportunity_id, as_of DESC);
CREATE INDEX opportunity_state_history_computed_idx ON opportunity_state_history (opportunity_id, computed_at DESC);

-- The current projection must exist in history (written in the same transaction).
ALTER TABLE opportunity_state ADD CONSTRAINT opportunity_state_in_history
    FOREIGN KEY (opportunity_id, version) REFERENCES opportunity_state_history (opportunity_id, version)
    DEFERRABLE INITIALLY DEFERRED;

-- +goose Down
ALTER TABLE opportunity_state DROP CONSTRAINT opportunity_state_in_history;
DROP TABLE opportunity_state_history;
DROP TABLE opportunity_state;
-- 'amount' claims cannot stay under the old CHECK, so Down deletes them. Rows that reference a claim with
-- ON DELETE RESTRICT (signals, decisions) would make a bare DELETE fail with an opaque foreign-key error halfway
-- through; say so up front instead. Nothing is deleted when the rollback is refused (the migration is one
-- transaction), so remove or re-point those references first and run Down again.
-- +goose StatementBegin
DO $$
BEGIN
    DELETE FROM claims WHERE field_path = 'amount';
EXCEPTION WHEN foreign_key_violation THEN
    RAISE EXCEPTION 'cannot roll back 0018: amount claims are still referenced by other rows (%); remove those references first', SQLERRM;
END
$$;
-- +goose StatementEnd
ALTER TABLE claims DROP CONSTRAINT claims_field_path_check;
ALTER TABLE claims ADD CONSTRAINT claims_field_path_check CHECK (field_path IN (
    'stage', 'health', 'owner', 'motion',
    'champion', 'champion_status', 'economic_buyer', 'buying_group.member', 'stakeholder_role',
    'blockers', 'objections', 'decision_criteria', 'decision_process',
    'commitment', 'next_milestone', 'next_meeting', 'relationship_risk',
    'product_use_case', 'commercial_issue', 'delegation', 'summary'));
