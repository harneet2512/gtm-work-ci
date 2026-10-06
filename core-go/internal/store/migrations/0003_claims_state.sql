-- +goose Up
-- Claims (sourced, competing assertions), the AccountState projection, its history,
-- the coalescing outbox and the extraction cache.

CREATE TABLE claims (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id          uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    opportunity_id      uuid,
    subject_person_id   uuid REFERENCES people (id) ON DELETE SET NULL,
    -- 'commitment' claims fold into AccountState.current_commitments; every other
    -- field_path folds into the AccountState field of the same name.
    field_path          text NOT NULL CHECK (field_path IN (
        'stage', 'health', 'owner', 'motion',
        'champion', 'champion_status', 'economic_buyer', 'buying_group.member', 'stakeholder_role',
        'blockers', 'objections', 'decision_criteria', 'decision_process',
        'commitment', 'next_milestone', 'next_meeting', 'relationship_risk',
        'product_use_case', 'commercial_issue', 'delegation', 'summary')),
    value               jsonb NOT NULL,
    value_hash          bytea GENERATED ALWAYS AS (decode(md5(value::text), 'hex')) STORED,
    standing            text NOT NULL CHECK (standing IN ('human_approved', 'crm_explicit', 'first_party_ai', 'third_party')),
    standing_rank       smallint GENERATED ALWAYS AS (
        CASE standing
            WHEN 'human_approved' THEN 4
            WHEN 'crm_explicit' THEN 3
            WHEN 'first_party_ai' THEN 2
            ELSE 1
        END) STORED,
    confidence          numeric(4, 3) NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    source_activity_id  uuid NOT NULL REFERENCES activities (id) ON DELETE RESTRICT,
    speaker_person_id   uuid REFERENCES people (id) ON DELETE SET NULL,
    evidence_quote      text CHECK (evidence_quote IS NULL OR length(evidence_quote) BETWEEN 1 AND 2000),
    occurred_at         timestamptz NOT NULL,
    extractor           text NOT NULL,  -- includes version, e.g. 'llm:deepseek-v4-flash@extract-v1'
    status              text NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'outranked', 'superseded', 'expired', 'rejected')),
    superseded_by       uuid REFERENCES claims (id) ON DELETE SET NULL,
    expires_at          timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT claims_ai_needs_evidence CHECK (standing <> 'first_party_ai' OR evidence_quote IS NOT NULL),
    CONSTRAINT claims_not_superseded_by_self CHECK (superseded_by IS NULL OR superseded_by <> id),
    CONSTRAINT claims_opportunity_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT
);
-- Winner selection: standing, then recency, then confidence; id makes the fold deterministic.
CREATE INDEX claims_winner_idx
    ON claims (account_id, field_path, standing_rank DESC, occurred_at DESC, confidence DESC, id)
    WHERE status = 'active';
CREATE INDEX claims_account_field_status_idx ON claims (account_id, field_path, status);
CREATE INDEX claims_subject_idx ON claims (subject_person_id) WHERE subject_person_id IS NOT NULL;
CREATE INDEX claims_speaker_idx ON claims (speaker_person_id) WHERE speaker_person_id IS NOT NULL;
CREATE INDEX claims_superseded_by_idx ON claims (superseded_by) WHERE superseded_by IS NOT NULL;
CREATE INDEX claims_opportunity_idx ON claims (opportunity_id) WHERE opportunity_id IS NOT NULL;
-- Re-running one extractor version over one activity never duplicates a claim
-- (writers use ON CONFLICT ON CONSTRAINT ... DO NOTHING).
ALTER TABLE claims ADD CONSTRAINT claims_dedupe_uniq
    UNIQUE NULLS NOT DISTINCT (source_activity_id, field_path, extractor, value_hash, subject_person_id);

-- Current projection (one row per account), rebuilt by the reducer as a pure fold.
-- Writers use optimistic locking: UPDATE ... WHERE version = $expected.
CREATE TABLE account_state (
    account_id        uuid PRIMARY KEY REFERENCES accounts (id) ON DELETE RESTRICT,
    opportunity_id    uuid,
    version           integer NOT NULL CHECK (version >= 0),
    as_of             timestamptz NOT NULL,
    computed_at       timestamptz NOT NULL DEFAULT now(),
    last_activity_id  uuid REFERENCES activities (id) ON DELETE SET NULL,
    state             jsonb NOT NULL,
    CONSTRAINT account_state_opportunity_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT
);
CREATE INDEX account_state_last_activity_idx ON account_state (last_activity_id);

CREATE TABLE state_history (
    id                    bigserial PRIMARY KEY,
    account_id            uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    version               integer NOT NULL CHECK (version >= 0),
    as_of                 timestamptz NOT NULL,
    computed_at           timestamptz NOT NULL DEFAULT now(),
    trigger_activity_ids  uuid[] NOT NULL DEFAULT '{}',
    state                 jsonb NOT NULL,
    CONSTRAINT state_history_version_uniq UNIQUE (account_id, version)
);
CREATE INDEX state_history_as_of_idx ON state_history (account_id, as_of DESC);

-- The current projection must exist in history (written in the same transaction).
ALTER TABLE account_state ADD CONSTRAINT account_state_in_history
    FOREIGN KEY (account_id, version) REFERENCES state_history (account_id, version)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE state_diffs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id    uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    from_version  integer NOT NULL CHECK (from_version >= 0),
    to_version    integer NOT NULL,
    is_material   boolean NOT NULL,
    changes       jsonb NOT NULL DEFAULT '[]'::jsonb,
    activity_ids  uuid[] NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT state_diffs_versions_increase CHECK (to_version > from_version),
    CONSTRAINT state_diffs_to_version_uniq UNIQUE (account_id, to_version),
    CONSTRAINT state_diffs_to_in_history FOREIGN KEY (account_id, to_version)
        REFERENCES state_history (account_id, version) DEFERRABLE INITIALLY DEFERRED,
    -- A material diff must list at least one change.
    CONSTRAINT state_diffs_material_has_changes CHECK (NOT is_material OR jsonb_array_length(changes) > 0)
);
CREATE INDEX state_diffs_account_time_idx ON state_diffs (account_id, created_at DESC);
CREATE INDEX state_diffs_activity_ids_gin ON state_diffs USING gin (activity_ids);

-- Coalescing outbox. At most one PENDING and one IN-FLIGHT job per account:
--  * ingest upserts the pending row (ON CONFLICT (account_id) WHERE claimed_at IS NULL),
--    appending activity_ids and extending due_at up to first_enqueued_at + max wait;
--  * a worker claims a due pending row only if no in-flight row exists for the account,
--    setting claimed_at and lease_expires_at (FOR UPDATE SKIP LOCKED);
--  * activity arriving during a recompute lands in a new pending row, never lost;
--  * an expired lease (crashed worker) is reclaimable.
CREATE TABLE recompute_jobs (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id         uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    due_at             timestamptz NOT NULL,
    first_enqueued_at  timestamptz NOT NULL,
    activity_ids       uuid[] NOT NULL DEFAULT '{}',
    claimed_at         timestamptz,
    claimed_by         text,
    lease_expires_at   timestamptz,
    attempts           integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error         text,
    CONSTRAINT recompute_jobs_due_after_enqueue CHECK (due_at >= first_enqueued_at),
    CONSTRAINT recompute_jobs_claim_has_lease CHECK ((claimed_at IS NULL) = (lease_expires_at IS NULL))
);
CREATE UNIQUE INDEX recompute_jobs_pending_uniq ON recompute_jobs (account_id) WHERE claimed_at IS NULL;
CREATE UNIQUE INDEX recompute_jobs_inflight_uniq ON recompute_jobs (account_id) WHERE claimed_at IS NOT NULL;
CREATE INDEX recompute_jobs_due_idx ON recompute_jobs (due_at) WHERE claimed_at IS NULL;
CREATE INDEX recompute_jobs_lease_idx ON recompute_jobs (lease_expires_at) WHERE claimed_at IS NOT NULL;

-- Activities whose account could not be resolved yet (the outbox is keyed by account).
CREATE TABLE unresolved_activities (
    activity_id  uuid PRIMARY KEY REFERENCES activities (id) ON DELETE CASCADE,
    reason       text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE extraction_cache (
    activity_id        uuid NOT NULL REFERENCES activities (id) ON DELETE CASCADE,
    extractor_version  text NOT NULL,
    model              text NOT NULL,
    output             jsonb NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (activity_id, extractor_version)
);

-- +goose Down
DROP TABLE extraction_cache;
DROP TABLE unresolved_activities;
DROP TABLE recompute_jobs;
DROP TABLE state_diffs;
ALTER TABLE account_state DROP CONSTRAINT account_state_in_history;
DROP TABLE state_history;
DROP TABLE account_state;
DROP TABLE claims;
