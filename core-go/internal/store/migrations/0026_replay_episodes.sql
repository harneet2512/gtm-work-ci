-- +goose Up
-- Episode Replay (HAR-129 §C bookkeeping; traceability/wp-episode-replay.md).
-- (Numbered 0026: after 0025_outbox_events (renumbered on merge with main).)

-- The replay cursor: how far a manifest's event sequence has been released. One row per manifest, created on
-- first advance; `released` is the position of the newest released episode (0 = only history is materialized).
CREATE TABLE demo_replays (
    manifest_id  uuid PRIMARY KEY REFERENCES demo_manifests (id) ON DELETE RESTRICT,
    released     integer NOT NULL DEFAULT 0 CHECK (released >= 0),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- One row per released episode, written at release time (idempotent: re-advancing an already-released
-- position returns this row and changes nothing). `material` records whether the event's trigger evaluation
-- made it eligible; a non-material episode carries the trigger's reason ("No action required"), never a
-- manufactured action. The link columns point at the real rows the pipeline wrote for the event.
CREATE TABLE demo_episodes (
    manifest_id         uuid NOT NULL REFERENCES demo_manifests (id) ON DELETE RESTRICT,
    position            integer NOT NULL CHECK (position >= 1),
    account_id          uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    event_id            uuid NOT NULL,                          -- the replay dataset's event id (uuid v5 of the ingest identity)
    source_event_id     uuid NOT NULL REFERENCES source_events (id) ON DELETE RESTRICT,
    activity_id         uuid NOT NULL REFERENCES activities (id) ON DELETE RESTRICT,
    state_diff_id       uuid REFERENCES state_diffs (id) ON DELETE RESTRICT,     -- the diff the event's activity was folded into (may be shared when coalesced)
    state_version       integer CHECK (state_version IS NULL OR state_version >= 1), -- the version visible at the episode bound, never a version past it
    material            boolean NOT NULL,
    no_action_reason    text CHECK (no_action_reason IS NULL OR length(no_action_reason) BETWEEN 1 AND 400),
    account_change_id   uuid REFERENCES account_changes (id) ON DELETE RESTRICT,
    decision_episode_id uuid REFERENCES decision_episodes (id) ON DELETE RESTRICT,
    graph_diff_id       bigint REFERENCES graph_projection_diffs (id) ON DELETE RESTRICT, -- the real projector diff naming the event
    coalesced           boolean NOT NULL DEFAULT false,                              -- the fold's diff also folded other activities: materiality is the shared verdict
    released_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (manifest_id, position),
    CONSTRAINT demo_episodes_event_once UNIQUE (manifest_id, event_id),
    CONSTRAINT demo_episodes_reason_when_no_action CHECK (
        (material AND no_action_reason IS NULL) OR (NOT material AND no_action_reason IS NOT NULL)),
    CONSTRAINT demo_episodes_state_version_known FOREIGN KEY (account_id, state_version)
        REFERENCES state_history (account_id, version) ON DELETE RESTRICT
);

-- +goose Down
DROP TABLE demo_episodes;
DROP TABLE demo_replays;
