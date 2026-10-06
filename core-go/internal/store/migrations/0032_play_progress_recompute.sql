-- +goose Up
-- HAR-145: the live Play pipeline and the edit recomputation read model.
-- (Numbered 0032: main's highest (0031 supervision) + 1; renumber on conflict.)

-- pipeline_stage_events: what the real pipeline did, stage by stage (contracts/schemas/pipeline_progress.v1.json).
-- A row exists only because the code that executes the stage wrote it: there are no timers and no placeholder
-- rows. A stage with no row is reported `waiting` by the read side. The row is updated in place as the stage
-- moves running -> completed/failed/skipped/unknown (every stage but evals) or running -> passed/warning/
-- failed/skipped/unknown (evals, the only stage that renders eval verdicts). `seq` is a change counter: it is
-- reassigned from one sequence on every insert and every update (trigger below), so a poller that remembers the
-- largest seq it saw detects any change; `updated_at` says when. A resumed Play re-runs a stage that did not
-- finish and bumps `attempt`. A completed, passed or warning stage is never re-run.
--
-- Play stages (ingest, resolve, graph, state) belong to a manifest; the run's stages (decide, evals, cliff)
-- belong to the AgentRun and are joined to a manifest at read time through demo_plays.activity_id, so the
-- writers need not know about manifests.
CREATE SEQUENCE pipeline_stage_events_change_seq;
CREATE TABLE pipeline_stage_events (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    seq              bigint NOT NULL DEFAULT nextval('pipeline_stage_events_change_seq'),
    manifest_id      uuid REFERENCES demo_manifests (id) ON DELETE CASCADE,
    run_id           uuid REFERENCES agent_runs (id) ON DELETE CASCADE,
    account_id       uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    stage            text NOT NULL CHECK (stage IN ('ingest', 'resolve', 'graph', 'state', 'decide', 'evals', 'cliff')),
    status           text NOT NULL CHECK (status IN ('running', 'completed', 'passed', 'warning', 'failed', 'skipped', 'unknown')),
    attempt          integer NOT NULL DEFAULT 1 CHECK (attempt >= 1),
    started_at       timestamptz NOT NULL,
    ended_at         timestamptz,
    refs             jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(refs) = 'object'),
    eval_result_ids  uuid[] NOT NULL DEFAULT '{}',
    failure_kind     text CHECK (failure_kind IN ('transport', 'contract', 'internal')),
    detail           text CHECK (length(detail) <= 500),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    -- `passed` and `warning` are eval verdicts: only the judging stage renders them. Every other stage that
    -- finished says `completed`: it ran to its end, which is not a judgment of quality.
    CONSTRAINT pipeline_stage_events_verdict_only_on_evals CHECK (stage = 'evals' OR status NOT IN ('passed', 'warning')),
    CONSTRAINT pipeline_stage_events_completed_not_on_evals CHECK (stage <> 'evals' OR status <> 'completed'),
    CONSTRAINT pipeline_stage_events_scope CHECK (manifest_id IS NOT NULL OR run_id IS NOT NULL),
    CONSTRAINT pipeline_stage_events_ended CHECK ((status = 'running') = (ended_at IS NULL)),
    CONSTRAINT pipeline_stage_events_ended_after_start CHECK (ended_at IS NULL OR ended_at >= started_at),
    -- a failure kind explains a failed or unknown stage, never a pass, a warning or a skip
    CONSTRAINT pipeline_stage_events_kind_only_on_failure CHECK (failure_kind IS NULL OR status IN ('failed', 'unknown')),
    -- a failed stage says why, except the judging stage, which fails only as a judgment (results failed)
    CONSTRAINT pipeline_stage_events_failed_says_why CHECK (status <> 'failed' OR stage = 'evals' OR failure_kind IS NOT NULL),
    -- a transport error while judging is `unknown`, never an eval FAIL (HAR-145 hard rule)
    CONSTRAINT pipeline_stage_events_eval_fail_is_a_verdict CHECK (
        stage <> 'evals' OR status <> 'failed' OR (failure_kind IS NULL AND cardinality(eval_result_ids) > 0)),
    CONSTRAINT pipeline_stage_events_eval_error_is_unknown CHECK (stage <> 'evals' OR failure_kind IS NULL OR status = 'unknown')
);
-- one row per stage of a manifest's Play and one per stage of a run
CREATE UNIQUE INDEX pipeline_stage_events_manifest_stage ON pipeline_stage_events (manifest_id, stage) WHERE run_id IS NULL;
CREATE UNIQUE INDEX pipeline_stage_events_run_stage ON pipeline_stage_events (run_id, stage) WHERE run_id IS NOT NULL;
CREATE INDEX pipeline_stage_events_seq ON pipeline_stage_events (seq);
-- +goose StatementBegin
CREATE FUNCTION pipeline_stage_events_bump_seq() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.seq := nextval('pipeline_stage_events_change_seq');
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER pipeline_stage_events_bump_seq BEFORE UPDATE ON pipeline_stage_events
    FOR EACH ROW EXECUTE FUNCTION pipeline_stage_events_bump_seq();

-- send_eval_results: which eval results are the send-time re-evaluation of the FINAL artifact of a human
-- strategy decision (HAR-139). eval_runs rows are content-addressed and a refused send persists its results
-- too, so the batch that belongs to the decision cannot be told from the table alone; the recomputation read
-- (HAR-97 E11) needs exactly this batch. Written in the send transaction, never updated.
CREATE TABLE send_eval_results (
    human_strategy_decision_id  uuid NOT NULL REFERENCES human_strategy_decisions (id) ON DELETE CASCADE,
    eval_run_id                 uuid NOT NULL REFERENCES eval_runs (id) ON DELETE CASCADE,
    -- the account state the send-time evaluation actually read (same on every row of a decision): its version and
    -- the sha-256 of its stored document. "State preserved" is a comparison of these stored values with the state
    -- the run read, never an assumption.
    state_version               integer NOT NULL CHECK (state_version >= 1),
    state_hash                  text NOT NULL CHECK (state_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (human_strategy_decision_id, eval_run_id)
);
CREATE INDEX send_eval_results_eval_idx ON send_eval_results (eval_run_id);

-- +goose Down
DROP TABLE send_eval_results;
DROP TRIGGER pipeline_stage_events_bump_seq ON pipeline_stage_events;
DROP FUNCTION pipeline_stage_events_bump_seq();
DROP TABLE pipeline_stage_events;
DROP SEQUENCE pipeline_stage_events_change_seq;
