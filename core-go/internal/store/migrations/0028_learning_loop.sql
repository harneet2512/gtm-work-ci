-- +goose Up
-- HAR-119 learning loop persistence.
--
-- (1) generator_feedback queues an explained HumanDelta finding for the account's next /v1/strategies
-- request (worker.yaml generator_feedback): an eval whose earlier failure the human's send edit repaired
-- is a proven correction, and the generator should apply it proactively on the next episode of the same
-- account. consumed_by_run_id marks the run that received it; a row is offered exactly once.
--
-- (2) evaluator_versions gains shadow_spec and knowledge_id. shadow_spec is the executable form of a
-- delta-derived candidate: the literal_changes it was learned from plus the account they bind to, so the
-- axis check can shadow-run against later drafts of that account. knowledge_id links the candidate to the
-- Knowledge row the unexplained delta seeded; the lifecycle of that row gates promotion.
--
-- (3) eval_backtest_runs persists every backtest of a candidate or shadow evaluator version against the
-- stored decision episodes and the WP16 gold sets: the episodes and gold payloads, the computed metrics,
-- the gate outcome and its reason.
--
-- (4) eval_promotions audits every lifecycle transition the loop performs (candidate -> shadow, shadow ->
-- active and any retirement), with the backtest that justified it and who decided.

CREATE TABLE generator_feedback (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id          uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    human_delta_id      uuid NOT NULL REFERENCES human_deltas (id) ON DELETE CASCADE,
    decision_episode_id uuid NOT NULL REFERENCES decision_episodes (id) ON DELETE CASCADE,
    eval_run_id         uuid NOT NULL REFERENCES eval_runs (id) ON DELETE CASCADE,
    eval_type           eval_type NOT NULL,
    instruction         text NOT NULL CHECK (length(btrim(instruction)) BETWEEN 1 AND 1000),
    consumed_by_run_id  uuid REFERENCES agent_runs (id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (human_delta_id, eval_run_id)
);
CREATE INDEX generator_feedback_pending_idx ON generator_feedback (account_id, created_at, id)
    WHERE consumed_by_run_id IS NULL;

ALTER TABLE evaluator_versions
    ADD COLUMN shadow_spec  jsonb CHECK (shadow_spec IS NULL OR jsonb_typeof (shadow_spec) = 'object'),
    ADD COLUMN knowledge_id uuid REFERENCES knowledge (id) ON DELETE RESTRICT,
    -- The account the version was learned on: ActiveVersions scopes the version tag runtime results
    -- carry to it, so one account's promoted criterion never re-versions another account's evals.
    -- NULL marks a global (manual or seeded) version.
    ADD COLUMN account_id   uuid REFERENCES accounts (id) ON DELETE CASCADE;
CREATE INDEX evaluator_versions_lifecycle_idx ON evaluator_versions (status)
    WHERE status IN ('candidate', 'shadow');

CREATE TABLE eval_backtest_runs (
    id         uuid PRIMARY KEY,
    evaluator  eval_type NOT NULL,
    version    integer NOT NULL,
    episodes   jsonb NOT NULL CHECK (jsonb_typeof (episodes) = 'object'),
    gold       jsonb NOT NULL CHECK (jsonb_typeof (gold) = 'object'),
    metrics    jsonb NOT NULL CHECK (jsonb_typeof (metrics) = 'object'),
    passed     boolean NOT NULL,
    reason     text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (evaluator, version) REFERENCES evaluator_versions (evaluator, version)
);
CREATE INDEX eval_backtest_runs_eval_idx ON eval_backtest_runs (evaluator, version, created_at DESC);

CREATE TABLE eval_promotions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    evaluator       eval_type NOT NULL,
    version         integer NOT NULL,
    from_status     text NOT NULL CHECK (from_status IN ('candidate', 'shadow', 'active')),
    to_status       text NOT NULL CHECK (to_status IN ('shadow', 'active', 'retired')),
    backtest_run_id uuid REFERENCES eval_backtest_runs (id) ON DELETE RESTRICT,
    decided_by      text NOT NULL,
    reason          text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (evaluator, version) REFERENCES evaluator_versions (evaluator, version)
);
CREATE INDEX eval_promotions_eval_idx ON eval_promotions (evaluator, version);

-- +goose Down
DROP TABLE eval_promotions;
DROP TABLE eval_backtest_runs;
DROP INDEX IF EXISTS evaluator_versions_lifecycle_idx;
ALTER TABLE evaluator_versions DROP COLUMN account_id, DROP COLUMN knowledge_id, DROP COLUMN shadow_spec;
DROP TABLE generator_feedback;
