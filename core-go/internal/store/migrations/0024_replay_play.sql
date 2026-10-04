-- +goose Up
-- HAR-124 (WP26): Play and the business-intelligence writer (ADR-0018).
-- (Numbered 0024: after 0023_har128_transition_policy; renumber on conflict.)

-- The transition status of the event, restated in the update (business_intelligence_update.v1.json `transition`).
-- NULL is "none": the event moved no relationship-state transition.
ALTER TABLE business_intelligence_updates
    ADD COLUMN transition jsonb CHECK (transition IS NULL OR jsonb_typeof(transition) = 'object');

-- Event N's payload is pinned in the manifest (held_out_event.v1.json payload_sha256, required by demo_manifest.v1.json):
-- Play verifies the replay dataset's payload against it. NOT VALID: manifests frozen before the pin keep their rows (Play
-- refuses them until they are frozen again); every new manifest must carry it.
ALTER TABLE demo_manifests ADD CONSTRAINT demo_manifests_held_out_payload_pinned
    CHECK (coalesce(held_out_event ->> 'payload_sha256' ~ '^[0-9a-f]{64}$', false)) NOT VALID;

-- A replay event is released once: at most one AccountChange per held-out event.
CREATE UNIQUE INDEX account_changes_held_out_once ON account_changes (held_out_event_id)
    WHERE held_out_event_id IS NOT NULL;

-- Play's own record, which makes it idempotent and resumable. A row is written after the event-N-invisible
-- assertion passed and before the event is released (status released); it completes in the same transaction
-- that writes the AccountChange and the update (status complete). Once a row exists the held-out event is
-- legitimately present, so the assertion no longer applies to it. Never updated otherwise, never deleted.
CREATE TABLE demo_plays (
    manifest_id        uuid PRIMARY KEY REFERENCES demo_manifests (id) ON DELETE RESTRICT,
    held_out_event_id  uuid NOT NULL UNIQUE,
    account_id         uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    status             text NOT NULL DEFAULT 'released' CHECK (status IN ('released', 'complete')),
    source_event_id    uuid REFERENCES source_events (id) ON DELETE RESTRICT,
    activity_id        uuid REFERENCES activities (id) ON DELETE RESTRICT,
    account_change_id  uuid REFERENCES account_changes (id) ON DELETE RESTRICT,
    started_at         timestamptz NOT NULL DEFAULT now(),
    completed_at       timestamptz,
    CONSTRAINT demo_plays_complete_has_change CHECK (
        (status = 'complete') = (account_change_id IS NOT NULL AND completed_at IS NOT NULL))
);

-- +goose Down
ALTER TABLE demo_manifests DROP CONSTRAINT demo_manifests_held_out_payload_pinned;
DROP TABLE demo_plays;
DROP INDEX account_changes_held_out_once;
ALTER TABLE business_intelligence_updates DROP COLUMN transition;
