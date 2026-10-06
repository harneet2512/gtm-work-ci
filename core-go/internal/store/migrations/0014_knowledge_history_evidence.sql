-- +goose Up
-- HAR-118 (WP20, ADR-0013): every knowledge status change records the evidence that caused it, and the
-- lifecycle history is append-only. Writers set, in the same transaction as the status change:
--   SELECT set_config('ghost.knowledge_reason', '...', true),
--          set_config('ghost.knowledge_evidence_kind', 'customer_reaction', true),
--          set_config('ghost.knowledge_evidence_ref', '<uuid>', true);
-- (Numbered 0014 at merge time: a migration's number is fixed only at merge time, as main's highest + 1 with no gaps.)

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION knowledge_record_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.status IS DISTINCT FROM OLD.status THEN
        INSERT INTO knowledge_status_history (knowledge_id, from_status, to_status, reason, evidence_kind, evidence_ref_id)
        VALUES (NEW.id, CASE WHEN TG_OP = 'INSERT' THEN NULL ELSE OLD.status END, NEW.status,
                coalesce(nullif(current_setting('ghost.knowledge_reason', true), ''), 'unspecified'),
                nullif(current_setting('ghost.knowledge_evidence_kind', true), ''),
                nullif(current_setting('ghost.knowledge_evidence_ref', true), '')::uuid);
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION knowledge_status_history_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'knowledge_status_history is append-only' USING ERRCODE = 'check_violation';
    END IF;
    IF current_setting('ghost.purge_knowledge', true) IS DISTINCT FROM 'on' THEN
        RAISE EXCEPTION 'knowledge_status_history rows are removed only by a purge job (ghost.purge_knowledge)'
            USING ERRCODE = 'check_violation';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER knowledge_status_history_no_change BEFORE UPDATE OR DELETE ON knowledge_status_history
    FOR EACH ROW EXECUTE FUNCTION knowledge_status_history_guard();
CREATE TRIGGER knowledge_status_history_no_truncate BEFORE TRUNCATE ON knowledge_status_history
    FOR EACH STATEMENT EXECUTE FUNCTION knowledge_status_history_guard();

-- +goose Down
DROP TRIGGER knowledge_status_history_no_truncate ON knowledge_status_history;
DROP TRIGGER knowledge_status_history_no_change ON knowledge_status_history;
DROP FUNCTION knowledge_status_history_guard();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION knowledge_record_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.status IS DISTINCT FROM OLD.status THEN
        INSERT INTO knowledge_status_history (knowledge_id, from_status, to_status, reason)
        VALUES (NEW.id, CASE WHEN TG_OP = 'INSERT' THEN NULL ELSE OLD.status END, NEW.status,
                coalesce(nullif(current_setting('ghost.knowledge_reason', true), ''), 'unspecified'));
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
