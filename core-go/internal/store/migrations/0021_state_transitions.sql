-- +goose Up
-- HAR-126 / ADR-0012: relationship state is earned by evidence. state_transitions mirrors
-- contracts/schemas/state_transition.v1.json (statuses, relationship-state vocabulary, fact shapes).
-- Every insert or update is appended to state_transition_history. CONFIRMED, REJECTED and closed
-- (stale UNRESOLVED) rows are terminal; transitions and their history are never updated out of band,
-- deleted or truncated except by an explicit purge (SET ghost.purge_transitions = 'on', ADR-0005).
-- Only the transition detector writes these tables.
CREATE TABLE state_transitions (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id            uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    opportunity_id        uuid,
    from_state            text NOT NULL CHECK (from_state IN ('unknown', 'NEW_LOGO', 'EXPANSION', 'RENEWAL', 'RECOVERY', 'REORG')),
    to_state_candidate    text CHECK (to_state_candidate IN ('NEW_LOGO', 'EXPANSION', 'RENEWAL', 'RECOVERY', 'REORG')),
    status                text NOT NULL CHECK (status IN ('CONFIRMED', 'CANDIDATE', 'UNRESOLVED', 'REJECTED')),
    trigger_activity_ids  uuid[] NOT NULL CHECK (cardinality(trigger_activity_ids) >= 1),
    supporting_facts      jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(supporting_facts) = 'array'),
    missing_facts         jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(missing_facts) = 'array'),
    contradicting_facts   jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(contradicting_facts) = 'array'),
    confidence            numeric(4, 3) NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    state_version         integer NOT NULL CHECK (state_version >= 0),
    rule_set_version      text NOT NULL CHECK (rule_set_version ~ '^transition_rules:v[0-9]+$'),
    first_observed_at     timestamptz NOT NULL,
    confirmed_at          timestamptz,
    rejected_at           timestamptz,
    closed_at             timestamptz,
    close_reason          text CHECK (close_reason IN ('stale')),
    last_updated_at       timestamptz NOT NULL,
    CONSTRAINT state_transitions_changes_state CHECK (to_state_candidate IS DISTINCT FROM from_state),
    CONSTRAINT state_transitions_target_iff_resolved CHECK ((status = 'UNRESOLVED') = (to_state_candidate IS NULL)),
    CONSTRAINT state_transitions_confirmed_at CHECK ((status = 'CONFIRMED') = (confirmed_at IS NOT NULL)),
    CONSTRAINT state_transitions_rejected_at CHECK ((status = 'REJECTED') = (rejected_at IS NOT NULL)),
    -- Contradicting evidence is kept on every status; a decisive one exists exactly on REJECTED rows.
    CONSTRAINT state_transitions_rejected_decisive CHECK (
        (status = 'REJECTED') = jsonb_path_exists(contradicting_facts, '$[*] ? (@.rejects == true)')),
    -- CONFIRMED: no unmet required fact. CANDIDATE: a supporting fact and an unmet required fact.
    CONSTRAINT state_transitions_confirmed_complete CHECK (
        status <> 'CONFIRMED' OR NOT jsonb_path_exists(missing_facts, '$[*] ? (@.required == true)')),
    CONSTRAINT state_transitions_candidate_partial CHECK (
        status <> 'CANDIDATE' OR (jsonb_array_length(supporting_facts) > 0
                                  AND jsonb_path_exists(missing_facts, '$[*] ? (@.required == true)'))),
    -- Fact lists hold what their names say; satisfied facts and contradictions rest on evidence.
    CONSTRAINT state_transitions_fact_shapes CHECK (
        NOT jsonb_path_exists(supporting_facts, '$[*] ? (@.satisfied != true || @.evidence_refs.size() < 1)')
        AND NOT jsonb_path_exists(missing_facts, '$[*] ? (@.satisfied != false)')),
    CONSTRAINT state_transitions_contradiction_shapes CHECK (NOT jsonb_path_exists(contradicting_facts,
        '$[*] ? (@.satisfied != true || @.required != false || !(exists(@.rejects))
                 || @.rejects.type() != "boolean" || @.evidence_refs.size() < 1)')),
    -- Only an UNRESOLVED transition is closed, and a closed one says why.
    CONSTRAINT state_transitions_closed CHECK ((closed_at IS NULL) = (close_reason IS NULL)
                                               AND (closed_at IS NULL OR status = 'UNRESOLVED')),
    CONSTRAINT state_transitions_time_order CHECK (
        last_updated_at >= first_observed_at
        AND (confirmed_at IS NULL OR confirmed_at >= first_observed_at)
        AND (rejected_at IS NULL OR rejected_at >= first_observed_at)
        AND (closed_at IS NULL OR closed_at >= first_observed_at)),
    CONSTRAINT state_transitions_opportunity_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT,
    -- The AccountState version the facts were evaluated against exists (written in the same transaction).
    CONSTRAINT state_transitions_state_in_history FOREIGN KEY (account_id, state_version)
        REFERENCES state_history (account_id, version) DEFERRABLE INITIALLY DEFERRED
);
-- At most one open transition per account; ambiguity is one UNRESOLVED row, and closing frees the slot.
CREATE UNIQUE INDEX state_transitions_one_open ON state_transitions (account_id)
    WHERE status IN ('CANDIDATE', 'UNRESOLVED') AND closed_at IS NULL;
CREATE INDEX state_transitions_account_idx ON state_transitions (account_id, last_updated_at DESC);
CREATE INDEX state_transitions_confirmed_idx ON state_transitions (account_id, confirmed_at DESC)
    WHERE status = 'CONFIRMED';
CREATE INDEX state_transitions_trigger_gin ON state_transitions USING gin (trigger_activity_ids);

CREATE TABLE state_transition_history (
    id               bigserial PRIMARY KEY,
    transition_id    uuid NOT NULL REFERENCES state_transitions (id) ON DELETE RESTRICT,
    status           text NOT NULL CHECK (status IN ('CONFIRMED', 'CANDIDATE', 'UNRESOLVED', 'REJECTED')),
    state_version    integer NOT NULL CHECK (state_version >= 0),
    snapshot         jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
    recorded_at      timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX state_transition_history_transition_idx ON state_transition_history (transition_id, id);

-- True only inside an explicit purge job (retention, test reset): SET ghost.purge_transitions = 'on'.
-- +goose StatementBegin
CREATE FUNCTION state_transitions_purging() RETURNS boolean LANGUAGE sql STABLE AS $$
    SELECT coalesce(current_setting('ghost.purge_transitions', true), '') = 'on'
$$;
-- +goose StatementEnd

-- Updates: terminal rows never change; identity never changes; the target changes only by being set
-- on an UNRESOLVED row (it becomes CANDIDATE/CONFIRMED) or cleared on a CANDIDATE that becomes UNRESOLVED.
-- Deletes are refused explicitly (not left to the history FK) unless purging.
-- +goose StatementBegin
CREATE FUNCTION state_transitions_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF state_transitions_purging() THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'state transition % cannot be deleted (explicit purge only)', OLD.id USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.status IN ('CONFIRMED', 'REJECTED') OR OLD.closed_at IS NOT NULL THEN
        RAISE EXCEPTION 'state transition % is terminal (%)', OLD.id, OLD.status USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.id <> OLD.id OR NEW.account_id <> OLD.account_id OR NEW.from_state <> OLD.from_state
       OR NEW.first_observed_at <> OLD.first_observed_at THEN
        RAISE EXCEPTION 'state transition % identity is immutable', OLD.id USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.status = 'UNRESOLVED' AND NEW.status = 'REJECTED' THEN
        RAISE EXCEPTION 'state transition % is UNRESOLVED: it closes or becomes CANDIDATE, never REJECTED', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.to_state_candidate IS DISTINCT FROM OLD.to_state_candidate
       AND OLD.to_state_candidate IS NOT NULL
       AND NOT (OLD.status = 'CANDIDATE' AND NEW.status = 'UNRESOLVED' AND NEW.to_state_candidate IS NULL) THEN
        RAISE EXCEPTION 'state transition % keeps its target unless it becomes UNRESOLVED', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER state_transitions_guard_trg BEFORE UPDATE OR DELETE ON state_transitions
    FOR EACH ROW EXECUTE FUNCTION state_transitions_guard();

-- +goose StatementBegin
CREATE FUNCTION state_transitions_record() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO state_transition_history (transition_id, status, state_version, snapshot)
    VALUES (NEW.id, NEW.status, NEW.state_version, to_jsonb(NEW));
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER state_transitions_history_trg AFTER INSERT OR UPDATE ON state_transitions
    FOR EACH ROW EXECUTE FUNCTION state_transitions_record();

-- History is append-only: never updated; deleted or truncated (either table) only by an explicit purge.
-- The purge setting stops accidents (a stray DELETE, a cascading TRUNCATE), not a deliberate bypass.
-- +goose StatementBegin
CREATE FUNCTION state_transitions_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF state_transitions_purging() THEN
        RETURN NULL;
    END IF;
    RAISE EXCEPTION '% on % is not allowed (explicit purge only)', TG_OP, TG_TABLE_NAME USING ERRCODE = 'check_violation';
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION state_transition_history_row_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND state_transitions_purging() THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'state_transition_history is append-only' USING ERRCODE = 'check_violation';
END $$;
-- +goose StatementEnd
CREATE TRIGGER state_transition_history_row_guard_trg BEFORE UPDATE OR DELETE ON state_transition_history
    FOR EACH ROW EXECUTE FUNCTION state_transition_history_row_guard();
CREATE TRIGGER state_transition_history_truncate_trg BEFORE TRUNCATE ON state_transition_history
    FOR EACH STATEMENT EXECUTE FUNCTION state_transitions_append_only();
CREATE TRIGGER state_transitions_truncate_trg BEFORE TRUNCATE ON state_transitions
    FOR EACH STATEMENT EXECUTE FUNCTION state_transitions_append_only();

-- Evidence types the transition rules need (ADR-0012); must equal claim.v1.json#/$defs/claimFieldPath (0017's list, which has 'amount', plus the three).
ALTER TABLE claims DROP CONSTRAINT claims_field_path_check;
ALTER TABLE claims ADD CONSTRAINT claims_field_path_check CHECK (field_path IN (
    'stage', 'health', 'owner', 'motion',
    'champion', 'champion_status', 'economic_buyer', 'buying_group.member', 'stakeholder_role',
    'blockers', 'objections', 'decision_criteria', 'decision_process',
    'commitment', 'next_milestone', 'next_meeting', 'relationship_risk',
    'product_use_case', 'commercial_issue', 'delegation', 'summary', 'amount',
    'org_change', 'expansion_need', 'business_value'));

-- New L1 eval type (contracts/evals/eval_catalog.json): must equal eval_result.v1.json#/$defs/evalType.
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

-- +goose Down
-- Lossy with data present (like 0010): rows using the new field paths or state_transition_support block
-- the narrower lists.
ALTER TABLE claims DROP CONSTRAINT claims_field_path_check;
ALTER TABLE claims ADD CONSTRAINT claims_field_path_check CHECK (field_path IN (
    'stage', 'health', 'owner', 'motion',
    'champion', 'champion_status', 'economic_buyer', 'buying_group.member', 'stakeholder_role',
    'blockers', 'objections', 'decision_criteria', 'decision_process',
    'commitment', 'next_milestone', 'next_meeting', 'relationship_risk',
    'product_use_case', 'commercial_issue', 'delegation', 'summary', 'amount'));
ALTER DOMAIN eval_type DROP CONSTRAINT eval_type_check;
ALTER DOMAIN eval_type ADD CONSTRAINT eval_type_check CHECK (VALUE IN (
    'recipient_correctness', 'date_commitment_consistency', 'pricing_integrity', 'crm_writeback',
    'duplicate_action', 'provenance_coverage', 'permission_policy',
    'buyer_readiness', 'cta_calibration', 'next_step_quality', 'stakeholder_selection',
    'stakeholder_coverage', 'economic_buyer_coverage', 'champion_strength', 'champion_continuity',
    'decision_process', 'business_case', 'momentum', 'action_stage_fit', 'expansion_readiness',
    'customer_risk_sensitivity', 'relationship_pressure', 'timing_cadence', 'next_action_quality',
    'grounding', 'commitment_consistency', 'state_change_relevance', 'channel_appropriateness',
    'rep_style', 'knowledge_applicability', 'exception_awareness', 'evidence_sufficiency',
    'trajectory', 'human_delta'));
DROP TABLE state_transition_history;
DROP TABLE state_transitions;
DROP FUNCTION state_transition_history_row_guard();
DROP FUNCTION state_transitions_append_only();
DROP FUNCTION state_transitions_record();
DROP FUNCTION state_transitions_guard();
DROP FUNCTION state_transitions_purging();
