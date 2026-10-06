-- +goose Up
-- HAR-145 operational metrics (contracts/schemas/worker_usage.v1.json, operational_metrics.v1.json).
-- (Numbered 0034: #69 takes 0032 and #68 takes 0033.)
--
-- The model worker returns what each request cost (`usage` on every response); core stores it per worker call, per run
-- step, so a run's model calls, tokens, tool calls, retries, latency by stage and cost are summed from rows rather than
-- estimated. This is a METRIC (HAR-97 M1-M5), never an eval: there is no verdict, score or blocking column, and nothing
-- here is read by an evaluator. Append-only by use: a retried or resumed run records the calls it really made.
--
-- run_step is the agent_run_steps.step the call belongs to (strategies, judge and revise happen under the orchestrator's
-- `draft` step; the human-delta labeler under `await_human`); NULL when no step owns the call. Tokens a provider did not
-- report (cached or reasoning tokens, cost) are stored as NULL, never 0, so a sum never invents a figure. usage_source says
-- whether a real provider answered (live) or recorded cassettes did (replay): a replayed call spent nothing, so core
-- stores it with zero tokens and no cost whatever the worker sent.
CREATE TABLE run_model_usage (
    id                   bigserial PRIMARY KEY,
    agent_run_id         uuid NOT NULL REFERENCES agent_runs (id) ON DELETE RESTRICT,
    run_step             text CHECK (run_step IN ('build_context', 'draft', 'crm_intent', 'await_human', 'execute')),
    stage                text NOT NULL CHECK (stage IN ('draft', 'strategies', 'judge', 'revise', 'human_delta', 'judgment_inference')),
    models               text[] NOT NULL DEFAULT '{}',
    model_calls          integer NOT NULL CHECK (model_calls >= 0),
    input_tokens         bigint NOT NULL CHECK (input_tokens >= 0),
    output_tokens        bigint NOT NULL CHECK (output_tokens >= 0),
    cached_input_tokens  bigint CHECK (cached_input_tokens IS NULL OR cached_input_tokens >= 0),
    reasoning_tokens     bigint CHECK (reasoning_tokens IS NULL OR reasoning_tokens >= 0),
    tool_calls           integer NOT NULL CHECK (tool_calls >= 0),
    retries              integer NOT NULL CHECK (retries >= 0),
    cost_usd             numeric(14, 6) CHECK (cost_usd IS NULL OR cost_usd >= 0),
    model_ms             bigint NOT NULL CHECK (model_ms >= 0),
    wall_ms              bigint NOT NULL CHECK (wall_ms >= 0),
    usage_source         text NOT NULL CHECK (usage_source IN ('live', 'replay')),
    recorded_at          timestamptz NOT NULL DEFAULT now(),
    -- A cached or reasoning count is a part of its total, never more than it.
    CONSTRAINT run_model_usage_cached_within_input CHECK (cached_input_tokens <= input_tokens),
    CONSTRAINT run_model_usage_reasoning_within_output CHECK (reasoning_tokens <= output_tokens),
    -- No model call, no tokens and no cost.
    CONSTRAINT run_model_usage_calls_explain_tokens CHECK (model_calls > 0 OR (input_tokens = 0 AND output_tokens = 0 AND cost_usd IS NULL)),
    -- A replayed call spent nothing.
    CONSTRAINT run_model_usage_replay_spends_nothing CHECK (usage_source = 'live' OR (model_calls = 0 AND input_tokens = 0 AND output_tokens = 0
        AND cached_input_tokens IS NULL AND reasoning_tokens IS NULL AND cost_usd IS NULL))
);
CREATE INDEX run_model_usage_run_idx ON run_model_usage (agent_run_id, id);

-- Which stage wrote an EvalResult: 'generation' (judged with the candidates) or 'send' (the send-time re-evaluation of
-- the final artifact, HAR-139). Both share a draft index and the run's replay-clock time, so only a tag can tell them
-- apart; the control plane attaches 'send' results to the recomputed action instead of to the candidates. A send-time
-- result identical to a generation-time one is the same row (content-hashed id) and keeps its 'generation' tag.
ALTER TABLE eval_runs ADD COLUMN phase text NOT NULL DEFAULT 'generation' CHECK (phase IN ('generation', 'send'));

-- A knowledge object's preconditions (situation_signature), applicability conditions and exceptions are written once. The
-- lifecycle only counts evidence and moves status, and the control plane's knowledge-mutation read shows these three
-- from the knowledge row, which is the version in force at the time of every mutation only while this holds. Enforce it.
-- +goose StatementBegin
CREATE FUNCTION knowledge_conditions_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.situation_signature IS DISTINCT FROM OLD.situation_signature
       OR NEW.applicability_conditions IS DISTINCT FROM OLD.applicability_conditions
       OR NEW.exceptions IS DISTINCT FROM OLD.exceptions THEN
        RAISE EXCEPTION 'knowledge %: preconditions, applicability conditions and exceptions are not rewritten after creation', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER knowledge_conditions_immutable BEFORE UPDATE ON knowledge
    FOR EACH ROW EXECUTE FUNCTION knowledge_conditions_immutable();

-- +goose Down
DROP TRIGGER knowledge_conditions_immutable ON knowledge;
DROP FUNCTION knowledge_conditions_immutable();
ALTER TABLE eval_runs DROP COLUMN phase;
DROP INDEX run_model_usage_run_idx;
DROP TABLE run_model_usage;
