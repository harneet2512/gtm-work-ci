-- +goose Up
-- HAR-97 Bucket 2 (D1-D10). (1) The real JudgmentInference producer stores what the model classified: the edit
-- class, the signal strength, the explicit instructions and whether it said unknown (judgment_inference.v1.json).
ALTER TABLE judgment_inferences
    ADD COLUMN edit_class            text[] NOT NULL DEFAULT '{}' CHECK (edit_class <@ ARRAY[
        'factual', 'state', 'strategy', 'stakeholder', 'timing', 'cta', 'style', 'recipient', 'risk', 'tone', 'wording', 'new_info', 'unknown']),
    ADD COLUMN signal_strength       text CHECK (signal_strength IN ('weak', 'moderate', 'strong')),
    ADD COLUMN explicit_instructions jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(explicit_instructions) = 'array'),
    ADD COLUMN confidence            numeric(4, 3) CHECK (confidence BETWEEN 0 AND 1),
    ADD COLUMN is_unknown            boolean NOT NULL DEFAULT false;

-- (2) One stored result per gate judgment of an episode (gate_result.v1.json). Rule R1 is a table check: a pass,
-- warn or fail needs at least one evidence ref, so a result with no evidence can only be unknown.
CREATE TABLE gate_results (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    decision_episode_id uuid NOT NULL,
    gate                text NOT NULL CHECK (gate ~ '^(B[1-9]|D([1-9]|10)|S[1-5])$'),
    sub_gate            text NOT NULL DEFAULT '' CHECK (length(sub_gate) <= 80),
    label               text NOT NULL DEFAULT '' CHECK (label = '' OR label ~ '^[A-Z][A-Z_]*$'),
    judged_type         text NOT NULL CHECK (judged_type ~ '^[A-Z][A-Za-z]+$'),
    judged_id           text NOT NULL CHECK (length(judged_id) BETWEEN 1 AND 200),
    span_id             text NOT NULL CHECK (span_id ~ '^[a-z_]+:[A-Za-z0-9._-]+$'),
    verdict             text NOT NULL CHECK (verdict IN ('pass', 'warn', 'fail', 'unknown')),
    question            text NOT NULL,
    observed            text NOT NULL,
    why                 text NOT NULL,
    evidence_refs       jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(evidence_refs) = 'array'),
    improves            text NOT NULL,
    grader              jsonb NOT NULL,
    calibrated          boolean NOT NULL DEFAULT false CHECK (NOT calibrated),
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT gate_results_r1 CHECK (verdict = 'unknown' OR jsonb_array_length(evidence_refs) >= 1),
    CONSTRAINT gate_results_unique UNIQUE (decision_episode_id, gate, sub_gate, judged_type, judged_id)
);
CREATE INDEX gate_results_episode ON gate_results (decision_episode_id, gate);

-- (3) The stored DecisionRanking (decision_ranking.v1.json): the order, tier inputs and a reason per adjacent pair.
CREATE TABLE decision_rankings (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_set_id        uuid NOT NULL UNIQUE,
    decision_episode_id    uuid NOT NULL,
    agent_run_id           uuid NOT NULL,
    order_ids              uuid[] NOT NULL CHECK (cardinality(order_ids) BETWEEN 1 AND 3),
    preferred_candidate_id uuid NOT NULL,
    tier_inputs            jsonb NOT NULL,
    pairwise_reasons       jsonb NOT NULL,
    abstained              boolean NOT NULL,
    model                  text,
    generated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT decision_rankings_preferred_first CHECK (order_ids[1] = preferred_candidate_id)
);

-- +goose Down
DROP TABLE decision_rankings;
DROP TABLE gate_results;
ALTER TABLE judgment_inferences DROP COLUMN is_unknown, DROP COLUMN confidence, DROP COLUMN explicit_instructions,
    DROP COLUMN signal_strength, DROP COLUMN edit_class;
