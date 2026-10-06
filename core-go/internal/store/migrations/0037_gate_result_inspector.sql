-- +goose Up
-- HAR-149 (eval inspector): persist more of what the gates already compute, so the UI can say how a result was produced
-- without inventing anything. criteria are the per-criterion results folded into the verdict (a model judge's
-- dimensions, a deterministic gate's assertions). control_effect and evaluator_version are computed at save time
-- (bucket2.ControlEffect, bucket2.EvaluatorVersion). lineage links a re-run to the result it replaced. The metrics are
-- NULL when the backend did not measure them: never 0.
ALTER TABLE gate_results
    ADD COLUMN criteria          jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(criteria) = 'array'),
    ADD COLUMN control_effect    text NOT NULL DEFAULT 'RECORD ONLY' CHECK (control_effect IN (
        'OBSERVE', 'CONTINUE', 'RANK', 'RECOMPUTE', 'RETRY', 'BLOCK', 'ESCALATE', 'MARK UNKNOWN', 'RECORD ONLY')),
    ADD COLUMN evaluator_version text NOT NULL DEFAULT '',
    ADD COLUMN lineage           jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(lineage) = 'object'),
    ADD COLUMN latency_ms        bigint CHECK (latency_ms >= 0),
    ADD COLUMN model_calls       bigint CHECK (model_calls >= 0),
    ADD COLUMN tokens            bigint CHECK (tokens >= 0),
    ADD COLUMN cost_usd          numeric(12, 6) CHECK (cost_usd >= 0);

-- Results stored before this migration get the effect their verdict has under the same rules (never a guess).
UPDATE gate_results SET control_effect = CASE
    WHEN verdict = 'unknown' THEN 'MARK UNKNOWN'
    WHEN gate = 'D8' AND sub_gate IN ('recipients', 'no_stale_content') AND grader->>'kind' = 'deterministic' AND verdict = 'fail' THEN 'BLOCK'
    WHEN gate = 'D8' AND sub_gate IN ('recipients', 'no_stale_content') AND grader->>'kind' = 'deterministic' AND verdict = 'pass' THEN 'CONTINUE'
    WHEN gate = 'S2' AND verdict IN ('warn', 'fail') THEN 'MARK UNKNOWN'
    ELSE 'RECORD ONLY' END;

-- +goose Down
ALTER TABLE gate_results DROP COLUMN cost_usd, DROP COLUMN tokens, DROP COLUMN model_calls, DROP COLUMN latency_ms,
    DROP COLUMN lineage, DROP COLUMN evaluator_version, DROP COLUMN control_effect, DROP COLUMN criteria;
