-- +goose Up
-- HAR-129 integrity guards on the tables of 0019 (ADR-0017), split out to keep migrations under 400 lines.

-- An episode only moves forward; it is chosen only once a strategy decision exists and judged only once the
-- human has answered the inference (confirmed or corrected, not pending).
-- +goose StatementBegin
CREATE FUNCTION decision_episodes_check_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    order_ text[] := ARRAY['awaiting_choice', 'chosen', 'decided', 'judged'];
    verdict text;
BEGIN
    IF TG_OP = 'UPDATE' AND array_position(order_, NEW.status) < array_position(order_, OLD.status) THEN
        RAISE EXCEPTION 'episode status cannot move from % back to %', OLD.status, NEW.status
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.status IN ('chosen', 'judged')
       AND NOT EXISTS (SELECT 1 FROM human_strategy_decisions WHERE decision_episode_id = NEW.id) THEN
        RAISE EXCEPTION 'episode status % needs a human strategy decision', NEW.status
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.status = 'judged' THEN
        SELECT human_verdict INTO verdict FROM judgment_inferences WHERE decision_episode_id = NEW.id;
        IF verdict IS NULL OR verdict = 'pending' THEN
            RAISE EXCEPTION 'episode status judged needs an answered judgment inference'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER decision_episodes_status_rules BEFORE INSERT OR UPDATE OF status ON decision_episodes
    FOR EACH ROW EXECUTE FUNCTION decision_episodes_check_status();

-- An account change's snapshots are account-level (state_diffs are) and equal the diff's from and to versions.
-- +goose StatementBegin
CREATE FUNCTION account_changes_check_refs() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    from_v integer;
    to_v integer;
BEGIN
    SELECT from_version, to_version INTO from_v, to_v FROM state_diffs WHERE id = NEW.state_diff_id;
    IF NEW.previous_state_ref ->> 'opportunity_id' IS NOT NULL OR NEW.current_state_ref ->> 'opportunity_id' IS NOT NULL
       OR NEW.previous_state_ref ->> 'account_id' IS DISTINCT FROM NEW.account_id::text
       OR NEW.current_state_ref ->> 'account_id' IS DISTINCT FROM NEW.account_id::text THEN
        RAISE EXCEPTION 'account change refs must be account-level refs of the change''s account'
            USING ERRCODE = 'check_violation';
    END IF;
    IF (NEW.previous_state_ref ->> 'version')::integer IS DISTINCT FROM from_v
       OR (NEW.current_state_ref ->> 'version')::integer IS DISTINCT FROM to_v THEN
        RAISE EXCEPTION 'account change refs must match the state diff''s from and to versions'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER account_changes_ref_rules BEFORE INSERT OR UPDATE ON account_changes
    FOR EACH ROW EXECUTE FUNCTION account_changes_check_refs();

-- +goose Down
DROP TRIGGER account_changes_ref_rules ON account_changes;
DROP FUNCTION account_changes_check_refs();
DROP TRIGGER decision_episodes_status_rules ON decision_episodes;
DROP FUNCTION decision_episodes_check_status();
