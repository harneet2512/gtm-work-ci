-- +goose Up
-- WP5 (HAR-103): provenance, re-resolution bookkeeping and the identity back-fill index.
-- (The first_party_record standing is migration 0010.)

-- Provenance (HAR-96 §5, "inspect why a record was attached"): which cue, rule or source file
-- produced a mapping or an edge.
ALTER TABLE entity_source_mappings ADD COLUMN evidence jsonb;
ALTER TABLE relationships ADD COLUMN evidence jsonb;

-- Re-resolution bookkeeping: rows that keep failing are retried with a growing delay so newer
-- parked activities are always reached.
ALTER TABLE unresolved_activities ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0);
ALTER TABLE unresolved_activities ADD COLUMN last_tried_at timestamptz;

-- Identity back-fill re-points participant rows by raw identity inside the ingest transaction.
CREATE INDEX activity_participants_raw_identity_idx ON activity_participants (raw_identity);

-- +goose Down
DROP INDEX activity_participants_raw_identity_idx;
ALTER TABLE unresolved_activities DROP COLUMN last_tried_at;
ALTER TABLE unresolved_activities DROP COLUMN attempts;
ALTER TABLE relationships DROP COLUMN evidence;
ALTER TABLE entity_source_mappings DROP COLUMN evidence;
