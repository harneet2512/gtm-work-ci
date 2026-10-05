-- +goose Up
-- Eval disputes (HAR-97 E19 grader validity; contracts/schemas/eval_dispute.v1.json; traceability wp-eval-pages).
-- A human says one EvalResult is wrong ("This eval is wrong", Slack or web). The row snapshots what was disputed
-- (verdict, blocking flag, eval type and version) from eval_runs at insert time, so eval-of-evals (E19-E22) counts
-- false passes and false blocks without joining back. Append-only: a dispute is never rewritten.
-- (Numbered 0030: origin/main ends at 0029_knowledge_key_alloc.sql.)

CREATE TABLE eval_disputes (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    eval_result_id     uuid NOT NULL REFERENCES eval_runs (id) ON DELETE RESTRICT,
    agent_run_id       uuid NOT NULL REFERENCES agent_runs (id) ON DELETE RESTRICT,
    draft_index        integer NOT NULL CHECK (draft_index >= 1),
    eval_type          eval_type NOT NULL,
    eval_version       text NOT NULL CHECK (eval_version ~ '^[a-z_]+:v[0-9]+$'),
    disputed_verdict   text NOT NULL CHECK (disputed_verdict IN ('pass', 'warn', 'fail', 'abstain')),
    disputed_blocking  boolean NOT NULL,
    expected_verdict   text CHECK (expected_verdict IN ('pass', 'warn', 'fail', 'abstain', 'not_relevant')),
    reason             text NOT NULL CHECK (length(btrim(reason)) >= 1 AND length(reason) <= 2000),
    surface            text NOT NULL CHECK (surface IN ('web', 'slack', 'mcp', 'api')),
    actor_person_id    uuid REFERENCES people (id) ON DELETE RESTRICT,
    actor_label        text NOT NULL CHECK (length(actor_label) BETWEEN 1 AND 200),
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT eval_disputes_only_fail_blocks CHECK (NOT disputed_blocking OR disputed_verdict = 'fail'),
    CONSTRAINT eval_disputes_expected_differs CHECK (expected_verdict IS DISTINCT FROM disputed_verdict)
);
CREATE INDEX eval_disputes_result_idx ON eval_disputes (eval_result_id, created_at);
CREATE INDEX eval_disputes_type_idx ON eval_disputes (eval_type, created_at);
-- A redelivered Slack action or a double click repeats the same dispute: it converges on one row.
CREATE UNIQUE INDEX eval_disputes_once ON eval_disputes
    (eval_result_id, surface, actor_label, md5(reason), (coalesce(expected_verdict, '')));

-- +goose StatementBegin
CREATE FUNCTION eval_disputes_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'eval_disputes is append-only' USING ERRCODE = 'check_violation';
END $$;
-- +goose StatementEnd
CREATE TRIGGER eval_disputes_no_update BEFORE UPDATE ON eval_disputes
    FOR EACH ROW EXECUTE FUNCTION eval_disputes_append_only();

-- +goose Down
DROP TRIGGER eval_disputes_no_update ON eval_disputes;
DROP FUNCTION eval_disputes_append_only();
DROP TABLE eval_disputes;
