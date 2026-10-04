-- +goose Up
-- Recompute liveness (HAR-104): a job that exhausted its attempts is PARKED explicitly (parked_at),
-- not by a magic claimed_by value, and a poison activity is QUARANTINED instead of blocking its account.
-- (0011 is reserved for WP5; whichever work package merges second renumbers.)
ALTER TABLE recompute_jobs ADD COLUMN parked_at timestamptz;
ALTER TABLE recompute_jobs ADD CONSTRAINT recompute_jobs_parked_is_claimed CHECK (parked_at IS NULL OR claimed_at IS NOT NULL);
CREATE INDEX recompute_jobs_parked_idx ON recompute_jobs (parked_at) WHERE parked_at IS NOT NULL;

CREATE TABLE quarantined_activities (
    activity_id     uuid PRIMARY KEY REFERENCES activities (id) ON DELETE CASCADE,
    account_id      uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    reason          text NOT NULL,
    permanent       boolean NOT NULL,
    attempts        integer NOT NULL CHECK (attempts >= 0),
    quarantined_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX quarantined_activities_account_idx ON quarantined_activities (account_id, quarantined_at);

-- +goose Down
DROP TABLE quarantined_activities;
DROP INDEX recompute_jobs_parked_idx;
ALTER TABLE recompute_jobs DROP CONSTRAINT recompute_jobs_parked_is_claimed;
ALTER TABLE recompute_jobs DROP COLUMN parked_at;
