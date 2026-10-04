-- +goose Up
-- Origin markers on raw events (WP32, HAR-131; contracts source_event.v1.json, common.v1.json
-- recordOrigin / recordProvenance). Synthetic and dataset-replay events say where they come from,
-- so a synthetic record can always be told apart from a base one. The marker stays on
-- source_events (audit): activities, state and agent context deliberately do not carry it.
-- (Migration numbers are assigned at merge time; renumber on conflict.)
ALTER TABLE source_events ADD COLUMN origin text CHECK (origin IN ('live', 'dataset', 'synthetic'));
ALTER TABLE source_events ADD COLUMN provenance text CHECK (provenance ~ '^[a-z][a-z0-9-]*:[A-Za-z0-9._-]{1,64}$');
-- Every comparison below is NULL-safe: a CHECK that evaluates to NULL passes, so a bare
-- `origin IN (...)` would let a provenance through with no origin.
ALTER TABLE source_events ADD CONSTRAINT source_events_provenance_iff_not_live CHECK (
    (provenance IS NULL AND coalesce(origin, 'live') = 'live')
    OR (provenance IS NOT NULL AND coalesce(origin, '') IN ('dataset', 'synthetic')));
ALTER TABLE source_events ADD CONSTRAINT source_events_synthetic_provenance CHECK (
    (origin IS DISTINCT FROM 'synthetic' OR provenance ~ '^synthetic:v[1-9][0-9]*$')
    AND (provenance IS NULL OR provenance NOT LIKE 'synthetic:%' OR origin IS NOT DISTINCT FROM 'synthetic'));
CREATE INDEX source_events_origin_idx ON source_events (origin) WHERE origin IS NOT NULL;

-- +goose Down
DROP INDEX source_events_origin_idx;
ALTER TABLE source_events DROP CONSTRAINT source_events_synthetic_provenance;
ALTER TABLE source_events DROP CONSTRAINT source_events_provenance_iff_not_live;
ALTER TABLE source_events DROP COLUMN provenance;
ALTER TABLE source_events DROP COLUMN origin;
