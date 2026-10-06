-- +goose Up
-- Decision-evals contracts (WP-A, HAR-97): an EvalResult now says WHAT it judged and WHERE it attaches, and `unknown`
-- is a verdict. Numbered 0035: origin/main ends at 0034_run_model_usage.sql.
--
-- judged_object / span_id (eval_result.v1.json): NULL on a row written before this migration (the result then names
-- no object; it is never given an invented one) and set on every row written after it by core's evalstore. The shape
-- is the contract's: {"type": PascalCase contract name, "id": text}; span_id is "<span kind>:<agent run id>".
ALTER TABLE eval_runs
    ADD COLUMN judged_object jsonb,
    ADD COLUMN span_id       text,
    ADD CONSTRAINT eval_runs_judged_object_shape CHECK (
        judged_object IS NULL OR (
            jsonb_typeof(judged_object) = 'object'
            AND judged_object ?& ARRAY['type', 'id']
            AND jsonb_typeof(judged_object -> 'type') = 'string' AND judged_object ->> 'type' ~ '^[A-Z][A-Za-z]+$'
            AND jsonb_typeof(judged_object -> 'id') = 'string' AND length(judged_object ->> 'id') BETWEEN 1 AND 200)),
    ADD CONSTRAINT eval_runs_span_id_shape CHECK (span_id IS NULL OR span_id ~ '^[a-z_]+:[A-Za-z0-9._-]+$'),
    ADD CONSTRAINT eval_runs_object_and_span_together CHECK ((judged_object IS NULL) = (span_id IS NULL));

-- unknown: the evidence cannot settle the judgment (and rule R1: a semantic pass that cites nothing). abstain stays
-- valid: it is the older spelling of the same verdict, read as unknown everywhere.
ALTER TABLE eval_runs DROP CONSTRAINT eval_runs_verdict_check;
ALTER TABLE eval_runs ADD CONSTRAINT eval_runs_verdict_check CHECK (verdict IN ('pass', 'fail', 'warn', 'unknown', 'abstain'));
ALTER TABLE eval_disputes DROP CONSTRAINT eval_disputes_disputed_verdict_check;
ALTER TABLE eval_disputes ADD CONSTRAINT eval_disputes_disputed_verdict_check
    CHECK (disputed_verdict IN ('pass', 'warn', 'fail', 'unknown', 'abstain'));
ALTER TABLE eval_disputes DROP CONSTRAINT eval_disputes_expected_verdict_check;
ALTER TABLE eval_disputes ADD CONSTRAINT eval_disputes_expected_verdict_check
    CHECK (expected_verdict IN ('pass', 'warn', 'fail', 'unknown', 'abstain', 'not_relevant'));

-- The split of grounding and timing_cadence by what they judge (contracts/evals/eval_areas.json): must equal
-- eval_result.v1.json#/$defs/evalType.
ALTER DOMAIN eval_type DROP CONSTRAINT eval_type_check;
ALTER DOMAIN eval_type ADD CONSTRAINT eval_type_check CHECK (VALUE IN (
    'recipient_correctness', 'date_commitment_consistency', 'pricing_integrity', 'crm_writeback',
    'duplicate_action', 'provenance_coverage', 'permission_policy', 'state_transition_support',
    'buyer_readiness', 'cta_calibration', 'next_step_quality', 'stakeholder_selection',
    'stakeholder_coverage', 'economic_buyer_coverage', 'champion_strength', 'champion_continuity',
    'decision_process', 'business_case', 'momentum', 'action_stage_fit', 'expansion_readiness',
    'customer_risk_sensitivity', 'relationship_pressure', 'timing_cadence', 'next_action_quality',
    'grounding', 'commitment_consistency', 'state_change_relevance', 'channel_appropriateness',
    'rep_style', 'knowledge_applicability', 'exception_awareness', 'evidence_sufficiency',
    'trajectory', 'human_delta',
    'decision_grounding', 'artifact_grounding', 'decision_timing', 'artifact_timing'));

-- +goose Down
-- Lossy with data present, like 0021: rows using the new verdict or eval types block the narrower lists.
ALTER DOMAIN eval_type DROP CONSTRAINT eval_type_check;
ALTER DOMAIN eval_type ADD CONSTRAINT eval_type_check CHECK (VALUE IN (
    'recipient_correctness', 'date_commitment_consistency', 'pricing_integrity', 'crm_writeback',
    'duplicate_action', 'provenance_coverage', 'permission_policy', 'state_transition_support',
    'buyer_readiness', 'cta_calibration', 'next_step_quality', 'stakeholder_selection',
    'stakeholder_coverage', 'economic_buyer_coverage', 'champion_strength', 'champion_continuity',
    'decision_process', 'business_case', 'momentum', 'action_stage_fit', 'expansion_readiness',
    'customer_risk_sensitivity', 'relationship_pressure', 'timing_cadence', 'next_action_quality',
    'grounding', 'commitment_consistency', 'state_change_relevance', 'channel_appropriateness',
    'rep_style', 'knowledge_applicability', 'exception_awareness', 'evidence_sufficiency',
    'trajectory', 'human_delta'));
ALTER TABLE eval_disputes DROP CONSTRAINT eval_disputes_expected_verdict_check;
ALTER TABLE eval_disputes ADD CONSTRAINT eval_disputes_expected_verdict_check
    CHECK (expected_verdict IN ('pass', 'warn', 'fail', 'abstain', 'not_relevant'));
ALTER TABLE eval_disputes DROP CONSTRAINT eval_disputes_disputed_verdict_check;
ALTER TABLE eval_disputes ADD CONSTRAINT eval_disputes_disputed_verdict_check
    CHECK (disputed_verdict IN ('pass', 'warn', 'fail', 'abstain'));
ALTER TABLE eval_runs DROP CONSTRAINT eval_runs_verdict_check;
ALTER TABLE eval_runs ADD CONSTRAINT eval_runs_verdict_check CHECK (verdict IN ('pass', 'fail', 'warn', 'abstain'));
ALTER TABLE eval_runs
    DROP CONSTRAINT eval_runs_object_and_span_together,
    DROP CONSTRAINT eval_runs_span_id_shape,
    DROP CONSTRAINT eval_runs_judged_object_shape,
    DROP COLUMN span_id,
    DROP COLUMN judged_object;
