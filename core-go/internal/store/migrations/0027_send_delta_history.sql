-- +goose Up
-- HAR-139: the send-time supervision writes. (1) decision_episodes gains the human_delta_id the
-- decision_episode.v1.json contract has always described (required for APPROVE_WITH_EDIT and
-- MANUAL_REPLACEMENT). (2) The human's verdict and notes on a judgment inference gain an append-only
-- history: judgment_inferences still carries the latest answer for reads, judgment_verdicts and
-- judgment_notes keep every accepted answer. (3) human_deltas.semantic_labels gets the shared-label
-- vocabulary CHECK judgment_inferences already has.

-- The delta belongs to the episode it supervises; RESTRICT keeps supervision records undeletable.
ALTER TABLE decision_episodes
    ADD COLUMN human_delta_id uuid REFERENCES human_deltas (id) ON DELETE RESTRICT;

-- Edits and manual replacements carry a delta; unchanged approvals and rejects never do. Enforced in the
-- row trigger (fires on INSERT/UPDATE), so episodes decided before this column existed stay valid.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION decision_episodes_check_action() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    d text;
    r uuid;
BEGIN
    IF NEW.human_decision_id IS NULL THEN
        IF NEW.human_delta_id IS NOT NULL THEN
            RAISE EXCEPTION 'a human delta needs a decided episode' USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;
    SELECT decision, agent_run_id INTO d, r FROM human_decisions WHERE id = NEW.human_decision_id;
    IF r IS DISTINCT FROM NEW.agent_run_id THEN
        RAISE EXCEPTION 'human decision belongs to a different run' USING ERRCODE = 'check_violation';
    END IF;
    IF NOT ((d = 'approve' AND NEW.human_action = 'APPROVE_UNCHANGED')
         OR (d = 'edit' AND NEW.human_action IN ('APPROVE_WITH_EDIT', 'MANUAL_REPLACEMENT'))
         OR (d = 'reject' AND NEW.human_action = 'REJECT')
         OR (d = 'ignore' AND NEW.human_action = 'IGNORE')) THEN
        RAISE EXCEPTION 'human_action % does not match decision %', NEW.human_action, d
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.human_action IN ('APPROVE_WITH_EDIT', 'MANUAL_REPLACEMENT') AND NEW.human_delta_id IS NULL THEN
        RAISE EXCEPTION 'an edited action needs its human delta' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.human_action NOT IN ('APPROVE_WITH_EDIT', 'MANUAL_REPLACEMENT') AND NEW.human_delta_id IS NOT NULL THEN
        RAISE EXCEPTION 'only an edited or replaced action carries a human delta' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- Every accepted verdict and every note, attributed, in insert order. The current answer stays on
-- judgment_inferences (the read contract is unchanged); these tables are the history. Updates and
-- deletes are refused: a correction of history is a new row, never a rewrite.
CREATE TABLE judgment_verdicts (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    judgment_inference_id   uuid NOT NULL REFERENCES judgment_inferences (id) ON DELETE RESTRICT,
    verdict                 text NOT NULL CHECK (verdict IN ('confirmed', 'corrected')),
    corrected_statement     text CHECK (corrected_statement IS NULL OR length(corrected_statement) BETWEEN 1 AND 1500),
    surface                 text NOT NULL CHECK (surface IN ('web', 'slack', 'mcp', 'api')),
    actor_person_id         uuid REFERENCES people (id) ON DELETE SET NULL,
    actor_label             text NOT NULL CHECK (length(actor_label) BETWEEN 1 AND 200),
    created_at              timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT judgment_verdicts_correction_has_statement CHECK (
        (verdict = 'corrected') = (corrected_statement IS NOT NULL))
);
CREATE INDEX judgment_verdicts_inference_idx ON judgment_verdicts (judgment_inference_id, created_at);

CREATE TABLE judgment_notes (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    judgment_inference_id   uuid NOT NULL REFERENCES judgment_inferences (id) ON DELETE RESTRICT,
    note                    text NOT NULL CHECK (length(note) BETWEEN 1 AND 2000),
    surface                 text NOT NULL CHECK (surface IN ('web', 'slack', 'mcp', 'api')),
    actor_person_id         uuid REFERENCES people (id) ON DELETE SET NULL,
    actor_label             text NOT NULL CHECK (length(actor_label) BETWEEN 1 AND 200),
    created_at              timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX judgment_notes_inference_idx ON judgment_notes (judgment_inference_id, created_at);

-- +goose StatementBegin
CREATE FUNCTION judgment_history_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'judgment history is append-only: write a new row, never rewrite one'
        USING ERRCODE = 'check_violation';
END $$;
-- +goose StatementEnd
CREATE TRIGGER judgment_verdicts_append_only BEFORE UPDATE OR DELETE ON judgment_verdicts
    FOR EACH ROW EXECUTE FUNCTION judgment_history_append_only();
CREATE TRIGGER judgment_notes_append_only BEFORE UPDATE OR DELETE ON judgment_notes
    FOR EACH ROW EXECUTE FUNCTION judgment_history_append_only();

-- The delta's semantic labels use the same vocabulary as the inference's (human_delta.v1.json).
ALTER TABLE human_deltas
    ADD CONSTRAINT human_deltas_semantic_labels_vocab CHECK (semantic_labels <@ ARRAY[
        'reduced_pressure', 'increased_pressure', 'kept_champion_involved', 'removed_unnecessary_stakeholders',
        'added_missing_stakeholder', 'delayed_cta', 'removed_cta', 'smaller_ask', 'larger_ask',
        'changed_channel', 'corrected_fact', 'deferred_to_buyer_timing', 'style_only']);

-- +goose Down
ALTER TABLE human_deltas DROP CONSTRAINT human_deltas_semantic_labels_vocab;
DROP TRIGGER judgment_notes_append_only ON judgment_notes;
DROP TRIGGER judgment_verdicts_append_only ON judgment_verdicts;
DROP FUNCTION judgment_history_append_only();
DROP TABLE judgment_notes;
DROP TABLE judgment_verdicts;
-- Restore the pre-0026 action check (0019's body, without the delta rules).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION decision_episodes_check_action() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    d text;
    r uuid;
BEGIN
    IF NEW.human_decision_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT decision, agent_run_id INTO d, r FROM human_decisions WHERE id = NEW.human_decision_id;
    IF r IS DISTINCT FROM NEW.agent_run_id THEN
        RAISE EXCEPTION 'human decision belongs to a different run' USING ERRCODE = 'check_violation';
    END IF;
    IF NOT ((d = 'approve' AND NEW.human_action = 'APPROVE_UNCHANGED')
         OR (d = 'edit' AND NEW.human_action IN ('APPROVE_WITH_EDIT', 'MANUAL_REPLACEMENT'))
         OR (d = 'reject' AND NEW.human_action = 'REJECT')
         OR (d = 'ignore' AND NEW.human_action = 'IGNORE')) THEN
        RAISE EXCEPTION 'human_action % does not match decision %', NEW.human_action, d
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER TABLE decision_episodes DROP COLUMN human_delta_id;
