-- +goose Up
-- ADR-0009: deterministic first-party records (calendar attendance, email headers, the vendor's
-- own org chart) are neither CRM-explicit nor AI-inferred. New standing first_party_record ranks
-- between them: human_approved 5 > crm_explicit 4 > first_party_record 3 > first_party_ai 2 > third_party 1.
ALTER TABLE claims DROP CONSTRAINT claims_standing_check;
ALTER TABLE claims ADD CONSTRAINT claims_standing_check CHECK (standing IN (
    'human_approved', 'crm_explicit', 'first_party_record', 'first_party_ai', 'third_party'));
DROP INDEX claims_winner_idx;
ALTER TABLE claims DROP COLUMN standing_rank;
ALTER TABLE claims ADD COLUMN standing_rank smallint GENERATED ALWAYS AS (
    CASE standing
        WHEN 'human_approved' THEN 5
        WHEN 'crm_explicit' THEN 4
        WHEN 'first_party_record' THEN 3
        WHEN 'first_party_ai' THEN 2
        ELSE 1
    END) STORED;
CREATE INDEX claims_winner_idx
    ON claims (account_id, field_path, standing_rank DESC, occurred_at DESC, confidence DESC, id)
    WHERE status = 'active';
ALTER TABLE claims ADD CONSTRAINT claims_record_is_rule CHECK (standing <> 'first_party_record' OR extractor LIKE 'rule:%');
ALTER TABLE relationships DROP CONSTRAINT relationships_standing_check;
ALTER TABLE relationships ADD CONSTRAINT relationships_standing_check CHECK (standing IN (
    'human_approved', 'crm_explicit', 'first_party_record', 'first_party_ai', 'third_party'));

-- +goose Down
ALTER TABLE claims DROP CONSTRAINT claims_record_is_rule;
ALTER TABLE relationships DROP CONSTRAINT relationships_standing_check;
ALTER TABLE relationships ADD CONSTRAINT relationships_standing_check CHECK (standing IN (
    'human_approved', 'crm_explicit', 'first_party_ai', 'third_party'));
DROP INDEX claims_winner_idx;
ALTER TABLE claims DROP COLUMN standing_rank;
ALTER TABLE claims DROP CONSTRAINT claims_standing_check;
ALTER TABLE claims ADD CONSTRAINT claims_standing_check CHECK (standing IN (
    'human_approved', 'crm_explicit', 'first_party_ai', 'third_party'));
ALTER TABLE claims ADD COLUMN standing_rank smallint GENERATED ALWAYS AS (
    CASE standing
        WHEN 'human_approved' THEN 4
        WHEN 'crm_explicit' THEN 3
        WHEN 'first_party_ai' THEN 2
        ELSE 1
    END) STORED;
CREATE INDEX claims_winner_idx
    ON claims (account_id, field_path, standing_rank DESC, occurred_at DESC, confidence DESC, id)
    WHERE status = 'active';
