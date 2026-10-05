-- +goose Up
-- HAR-129 demo objects (ADR-0017; contracts demo_manifest ... decision_episode). ReplayWorld and HeldOutEvent are not
-- tables: the world is derived and held-out events live in the manifest. FKs are RESTRICT (supervision records).
-- (Numbered 0019: main's highest + 1 at authoring; renumber on conflict.)
ALTER TABLE agent_run_drafts DROP CONSTRAINT agent_run_drafts_source_check;
ALTER TABLE agent_run_drafts DROP CONSTRAINT agent_run_drafts_first_from_agent;
ALTER TABLE agent_run_drafts
    ADD CONSTRAINT agent_run_drafts_source_check
        CHECK (source IN ('account_agent', 'revision_planner', 'strategy_generator')),
    ADD CONSTRAINT agent_run_drafts_source_index CHECK (
        (source = 'strategy_generator' AND revision_feedback = '[]'::jsonb)
        OR (source = 'account_agent' AND draft_index = 1)
        OR (source = 'revision_planner' AND draft_index > 1));
ALTER TABLE state_diffs ADD CONSTRAINT state_diffs_id_account_material_uniq UNIQUE (id, account_id, is_material);
-- The frozen demo case (HAR-129 section 1). Written once; history events 1..N-1 and the held-out event N apart.
CREATE TABLE demo_manifests (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id          uuid NOT NULL,
    opportunity_id      uuid NOT NULL,
    data_cutoff         timestamptz NOT NULL,
    events              jsonb NOT NULL CHECK (CASE WHEN jsonb_typeof(events) = 'array' THEN jsonb_array_length(events) >= 1 ELSE false END),
    held_out_event      jsonb NOT NULL CHECK (jsonb_typeof(held_out_event) = 'object'),
    held_out_event_id   uuid GENERATED ALWAYS AS ((held_out_event ->> 'event_id')::uuid) STORED NOT NULL,
    selection_expectations jsonb CHECK (selection_expectations IS NULL OR jsonb_typeof(selection_expectations) = 'object'),
    why_selected        text NOT NULL CHECK (length(why_selected) BETWEEN 1 AND 4000),
    mining              jsonb CHECK (mining IS NULL OR jsonb_typeof(mining) = 'object'),
    content_sha256      text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT demo_manifests_opportunity_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT,
    CONSTRAINT demo_manifests_held_out_is_next CHECK (coalesce(
        (held_out_event ->> 'replay_position')::integer = (events -> -1 -> 'event' ->> 'replay_position')::integer + 1, false)),
    -- Event N is not visible before Play: it carries no snapshot, diff or material flag.
    CONSTRAINT demo_manifests_held_out_has_no_state CHECK (NOT (held_out_event ?|
        ARRAY['state_after', 'state_diff_id', 'graph_diff_ref', 'is_material', 'material_dimensions']))
);
CREATE INDEX demo_manifests_account_idx ON demo_manifests (account_id, created_at DESC);
CREATE TABLE account_changes (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id            uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    opportunity_id        uuid,
    held_out_event_id     uuid,
    trigger_activity_ids  uuid[] NOT NULL CHECK (cardinality(trigger_activity_ids) >= 1),
    previous_state_ref    jsonb NOT NULL CHECK (jsonb_typeof(previous_state_ref) = 'object'),
    current_state_ref     jsonb NOT NULL CHECK (jsonb_typeof(current_state_ref) = 'object'),
    state_diff_id         uuid NOT NULL,
    graph_diff_ref        jsonb NOT NULL CHECK (jsonb_typeof(graph_diff_ref) = 'object'),
    material_change       boolean NOT NULL,
    evidence_refs         jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    created_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT account_changes_id_account_uniq UNIQUE (id, account_id),
    CONSTRAINT account_changes_opportunity_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT,
    CONSTRAINT account_changes_diff_material FOREIGN KEY (state_diff_id, account_id, material_change)
        REFERENCES state_diffs (id, account_id, is_material) ON DELETE RESTRICT,
    CONSTRAINT account_changes_material_has_evidence CHECK (NOT material_change OR jsonb_array_length(evidence_refs) >= 1)
);
CREATE INDEX account_changes_account_idx ON account_changes (account_id, created_at DESC);
CREATE TABLE business_intelligence_updates (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         uuid NOT NULL,
    opportunity_id     uuid,
    account_change_id  uuid NOT NULL UNIQUE,
    summary            text NOT NULL CHECK (length(summary) BETWEEN 1 AND 600),
    claims             jsonb NOT NULL CHECK (CASE WHEN jsonb_typeof(claims) = 'array' THEN jsonb_array_length(claims) >= 1 ELSE false END),
    why_it_matters     text NOT NULL CHECK (length(why_it_matters) BETWEEN 1 AND 1500),
    knowledge_refs     uuid[] NOT NULL DEFAULT '{}',
    account_map_ref    jsonb NOT NULL CHECK (jsonb_typeof(account_map_ref) = 'object'),
    model              text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT bi_updates_change_same_account FOREIGN KEY (account_change_id, account_id)
        REFERENCES account_changes (id, account_id) ON DELETE RESTRICT,
    CONSTRAINT bi_updates_opportunity_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT,
    CONSTRAINT bi_updates_every_claim_has_evidence
        CHECK (NOT jsonb_path_exists(claims, '$[*] ? (!exists(@.evidence_refs[*]))')),
    CONSTRAINT bi_updates_claims_carry_no_score
        CHECK (NOT jsonb_path_exists(claims, '$[*] ? (exists(@.confidence))'))
);
CREATE INDEX bi_updates_account_idx ON business_intelligence_updates (account_id, created_at DESC);
-- The episode is opened when the strategy set is generated and closes in stages. Episodes recorded before
-- (and runs without a strategy set) stay 'decided'. The strategy set, human strategy decision and
-- judgment inference point at the episode, not the other way round (no circular FKs).
ALTER TABLE decision_episodes
    ALTER COLUMN human_decision_id DROP NOT NULL,
    ALTER COLUMN human_action DROP NOT NULL,
    ALTER COLUMN final_draft_index DROP NOT NULL,
    ADD COLUMN status text NOT NULL DEFAULT 'decided'
        CHECK (status IN ('awaiting_choice', 'chosen', 'decided', 'judged')),
    ADD COLUMN held_out_event_id uuid,
    ADD COLUMN account_change_id uuid REFERENCES account_changes (id) ON DELETE RESTRICT,
    ADD COLUMN business_intelligence_update_id uuid REFERENCES business_intelligence_updates (id) ON DELETE RESTRICT,
    ADD COLUMN learning_scope text NOT NULL DEFAULT 'undetermined'
        CHECK (learning_scope IN ('undetermined', 'account_specific', 'reusable_candidate')),
    ADD CONSTRAINT decision_episodes_status_fields CHECK (
        (status IN ('awaiting_choice', 'chosen')
            AND human_decision_id IS NULL AND human_action IS NULL AND final_draft_index IS NULL)
        OR (status IN ('decided', 'judged')
            AND human_decision_id IS NOT NULL AND human_action IS NOT NULL AND final_draft_index IS NOT NULL)),
    ADD CONSTRAINT decision_episodes_replay_has_change CHECK (
        held_out_event_id IS NULL OR (account_change_id IS NOT NULL AND business_intelligence_update_id IS NOT NULL)),
    ADD CONSTRAINT decision_episodes_id_run_uniq UNIQUE (id, agent_run_id);
CREATE INDEX decision_episodes_account_change_idx ON decision_episodes (account_change_id) WHERE account_change_id IS NOT NULL;
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
-- Evals of one candidate (draft); the EvalResults inside are also eval_runs rows of the same run and draft.
CREATE TABLE eval_bundles (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_run_id  uuid NOT NULL,
    draft_index   integer NOT NULL CHECK (draft_index >= 1),
    items         jsonb NOT NULL CHECK (CASE WHEN jsonb_typeof(items) = 'array' THEN jsonb_array_length(items) >= 1 ELSE false END),
    generated_at  timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT eval_bundles_results_match_draft CHECK (NOT jsonb_path_exists(items,
        '$[*] ? (@.result.agent_run_id != $run || @.result.draft_index != $idx)',
        jsonb_build_object('run', agent_run_id::text, 'idx', draft_index))),
    CONSTRAINT eval_bundles_draft_once UNIQUE (agent_run_id, draft_index),
    CONSTRAINT eval_bundles_draft_exists FOREIGN KEY (agent_run_id, draft_index)
        REFERENCES agent_run_drafts (agent_run_id, draft_index) ON DELETE RESTRICT
);
CREATE TABLE strategy_sets (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    decision_episode_id   uuid NOT NULL UNIQUE,
    agent_run_id          uuid NOT NULL UNIQUE,
    account_id            uuid NOT NULL,
    opportunity_id        uuid,
    generated_at          timestamptz NOT NULL,
    state_ref             jsonb NOT NULL CHECK (jsonb_typeof(state_ref) = 'object'),
    state_diff_id         uuid REFERENCES state_diffs (id) ON DELETE RESTRICT,
    trigger_activity_ids  uuid[] NOT NULL CHECK (cardinality(trigger_activity_ids) >= 1),
    decision_guidance_id  uuid REFERENCES decision_guidance (id) ON DELETE RESTRICT,
    created_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT strategy_sets_id_run_uniq UNIQUE (id, agent_run_id),
    CONSTRAINT strategy_sets_episode_same_run FOREIGN KEY (decision_episode_id, agent_run_id)
        REFERENCES decision_episodes (id, agent_run_id) ON DELETE RESTRICT,
    CONSTRAINT strategy_sets_run_same_account FOREIGN KEY (agent_run_id, account_id)
        REFERENCES agent_runs (id, account_id) ON DELETE RESTRICT,
    CONSTRAINT strategy_sets_opportunity_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT
);
-- Contract id = candidate_id. ranking 1..3, unique per set, with the 3-row trigger: rankings 1, 2, 3 (section 6).
CREATE TABLE strategy_candidates (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_set_id       uuid NOT NULL,
    agent_run_id          uuid NOT NULL,
    draft_index           integer NOT NULL CHECK (draft_index >= 1),
    strategy_type         text NOT NULL CHECK (strategy_type ~ '^[a-z][a-z0-9_]*$' AND length(strategy_type) <= 80),
    title                 text NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
    description           text NOT NULL CHECK (length(description) BETWEEN 1 AND 300),
    ranking               integer NOT NULL CHECK (ranking BETWEEN 1 AND 3),
    preferred_by_agent    boolean NOT NULL,
    rationale             text NOT NULL CHECK (length(rationale) BETWEEN 1 AND 1500),
    state_refs            text[] NOT NULL DEFAULT '{}',
    evidence_refs         jsonb NOT NULL CHECK (CASE WHEN jsonb_typeof(evidence_refs) = 'array' THEN jsonb_array_length(evidence_refs) >= 1 ELSE false END),
    knowledge_refs        uuid[] NOT NULL DEFAULT '{}',
    action_type           text NOT NULL CHECK (action_type IN (
        'send_email', 'schedule_meeting', 'share_document', 'internal_note', 'wait', 'no_action')),
    to_recipients         jsonb NOT NULL CHECK (jsonb_typeof(to_recipients) = 'array'),
    cc_recipients         jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(cc_recipients) = 'array'),
    subject               text CHECK (length(subject) <= 300),
    full_action_artifact  jsonb NOT NULL CHECK (jsonb_typeof(full_action_artifact) = 'object'),
    preview               text NOT NULL CHECK (length(preview) BETWEEN 1 AND 600),
    eval_bundle_id        uuid NOT NULL UNIQUE REFERENCES eval_bundles (id) ON DELETE RESTRICT,
    created_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT strategy_candidates_set_id_uniq UNIQUE (strategy_set_id, id),
    CONSTRAINT strategy_candidates_ranking_once UNIQUE (strategy_set_id, ranking),
    CONSTRAINT strategy_candidates_draft_once UNIQUE (strategy_set_id, draft_index),
    CONSTRAINT strategy_candidates_set_same_run FOREIGN KEY (strategy_set_id, agent_run_id)
        REFERENCES strategy_sets (id, agent_run_id) ON DELETE RESTRICT,
    CONSTRAINT strategy_candidates_draft_exists FOREIGN KEY (agent_run_id, draft_index)
        REFERENCES agent_run_drafts (agent_run_id, draft_index) ON DELETE RESTRICT,
    CONSTRAINT strategy_candidates_prefers_the_first CHECK (preferred_by_agent = (ranking = 1)),
    CONSTRAINT strategy_candidates_subject_matches_artifact
        CHECK (subject IS NOT DISTINCT FROM (full_action_artifact ->> 'subject')),
    CONSTRAINT strategy_candidates_email_has_recipient
        CHECK (action_type <> 'send_email' OR jsonb_array_length(to_recipients) >= 1)
);
-- A strategy set holds exactly 3 candidates, checked at commit (same pattern as human_deltas).
-- +goose StatementBegin
CREATE FUNCTION strategy_sets_check_size() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    set_id uuid;
    n integer;
BEGIN
    IF TG_TABLE_NAME = 'strategy_sets' THEN
        set_id := NEW.id;
    ELSE
        set_id := NEW.strategy_set_id;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM strategy_sets WHERE id = set_id) THEN
        RETURN NULL;
    END IF;
    SELECT count(*) INTO n FROM strategy_candidates WHERE strategy_set_id = set_id;
    IF n <> 3 THEN
        RAISE EXCEPTION 'a strategy set holds exactly 3 candidates, found %', n USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER strategy_sets_size_check AFTER INSERT OR UPDATE ON strategy_sets
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION strategy_sets_check_size();
CREATE CONSTRAINT TRIGGER strategy_candidates_size_check AFTER INSERT OR UPDATE ON strategy_candidates
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION strategy_sets_check_size();
-- The human's choice and what they did with it; one row per episode, completed at send.
CREATE TABLE human_strategy_decisions (
    id                          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    decision_episode_id         uuid NOT NULL UNIQUE,
    agent_run_id                uuid NOT NULL,
    strategy_set_id             uuid NOT NULL UNIQUE,
    selected_candidate_id       uuid NOT NULL,
    original_agent_preference   uuid NOT NULL,
    surface                     text NOT NULL CHECK (surface IN ('web', 'slack', 'mcp', 'api')),
    actor_person_id             uuid REFERENCES people (id) ON DELETE SET NULL,
    actor_label                 text NOT NULL CHECK (length(actor_label) BETWEEN 1 AND 200),
    chosen_at                   timestamptz NOT NULL,
    final_to                    jsonb CHECK (final_to IS NULL OR jsonb_typeof(final_to) = 'array'),
    final_cc                    jsonb CHECK (final_cc IS NULL OR jsonb_typeof(final_cc) = 'array'),
    final_artifact              jsonb CHECK (final_artifact IS NULL OR jsonb_typeof(final_artifact) = 'object'),
    edits                       jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(edits) = 'array'),
    send_decision               text NOT NULL DEFAULT 'pending' CHECK (send_decision IN ('pending', 'send', 'discard')),
    send_decided_at             timestamptz,
    human_decision_id           uuid UNIQUE REFERENCES human_decisions (id) ON DELETE RESTRICT,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    -- the three ids a judgment inference must repeat unchanged
    CONSTRAINT human_strategy_decisions_choice_uniq
        UNIQUE (id, decision_episode_id, original_agent_preference, selected_candidate_id),
    CONSTRAINT human_strategy_decisions_episode_same_run FOREIGN KEY (decision_episode_id, agent_run_id)
        REFERENCES decision_episodes (id, agent_run_id) ON DELETE RESTRICT,
    CONSTRAINT human_strategy_decisions_set_same_run FOREIGN KEY (strategy_set_id, agent_run_id)
        REFERENCES strategy_sets (id, agent_run_id) ON DELETE RESTRICT,
    CONSTRAINT human_strategy_decisions_selected_in_set FOREIGN KEY (strategy_set_id, selected_candidate_id)
        REFERENCES strategy_candidates (strategy_set_id, id) ON DELETE RESTRICT,
    CONSTRAINT human_strategy_decisions_preferred_in_set FOREIGN KEY (strategy_set_id, original_agent_preference)
        REFERENCES strategy_candidates (strategy_set_id, id) ON DELETE RESTRICT,
    CONSTRAINT human_strategy_decisions_edits_have_artifact
        CHECK (jsonb_array_length(edits) = 0 OR final_artifact IS NOT NULL),
    CONSTRAINT human_strategy_decisions_send_is_complete CHECK (send_decision <> 'send' OR (
        final_artifact IS NOT NULL AND final_to IS NOT NULL AND jsonb_array_length(final_to) >= 1
        AND send_decided_at IS NOT NULL AND human_decision_id IS NOT NULL)),
    CONSTRAINT human_strategy_decisions_discard_is_complete CHECK (send_decision <> 'discard' OR (
        send_decided_at IS NOT NULL AND human_decision_id IS NOT NULL)),
    CONSTRAINT human_strategy_decisions_pending_is_open CHECK (send_decision <> 'pending' OR (
        send_decided_at IS NULL AND human_decision_id IS NULL))
);
-- Choosing never sends; the selection never changes; a send decision is final; the preference recorded is
-- the candidate ranked first; the HumanDecision written at send agrees with the send decision and edits.
-- +goose StatementBegin
CREATE FUNCTION human_strategy_decisions_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    preferred boolean;
    d text;
    r uuid;
BEGIN
    SELECT preferred_by_agent INTO preferred FROM strategy_candidates
     WHERE id = NEW.original_agent_preference AND strategy_set_id = NEW.strategy_set_id;
    IF preferred IS DISTINCT FROM true THEN
        RAISE EXCEPTION 'original_agent_preference must be the candidate Ghost ranked first'
            USING ERRCODE = 'check_violation';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF OLD.send_decision <> 'pending' THEN
            RAISE EXCEPTION 'the send decision is final' USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.selected_candidate_id <> OLD.selected_candidate_id
           OR NEW.original_agent_preference <> OLD.original_agent_preference THEN
            RAISE EXCEPTION 'the selection cannot change' USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    IF NEW.human_decision_id IS NOT NULL THEN
        SELECT decision, agent_run_id INTO d, r FROM human_decisions WHERE id = NEW.human_decision_id;
        IF r IS DISTINCT FROM NEW.agent_run_id THEN
            RAISE EXCEPTION 'human decision belongs to a different run' USING ERRCODE = 'check_violation';
        END IF;
        IF NOT ((NEW.send_decision = 'discard' AND d = 'reject')
             OR (NEW.send_decision = 'send' AND d = 'approve' AND jsonb_array_length(NEW.edits) = 0)
             OR (NEW.send_decision = 'send' AND d = 'edit' AND jsonb_array_length(NEW.edits) > 0)) THEN
            RAISE EXCEPTION 'human decision % does not match send decision % with % edits',
                d, NEW.send_decision, jsonb_array_length(NEW.edits) USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER human_strategy_decisions_rules BEFORE INSERT OR UPDATE ON human_strategy_decisions
    FOR EACH ROW EXECUTE FUNCTION human_strategy_decisions_check();
-- Ghost's inference of why the human chose what they chose, and the human's verdict on it.
CREATE TABLE judgment_inferences (
    id                          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    decision_episode_id         uuid NOT NULL UNIQUE,
    human_strategy_decision_id  uuid NOT NULL UNIQUE,
    agent_preference            uuid NOT NULL,
    human_choice                uuid NOT NULL,
    agreement                   text NOT NULL CHECK (agreement IN ('agreed', 'overrode')),
    inferred_statement          text NOT NULL CHECK (length(inferred_statement) BETWEEN 1 AND 1500),
    semantic_labels             text[] NOT NULL DEFAULT '{}' CHECK (semantic_labels <@ ARRAY[
        'reduced_pressure', 'increased_pressure', 'kept_champion_involved', 'removed_unnecessary_stakeholders',
        'added_missing_stakeholder', 'delayed_cta', 'removed_cta', 'smaller_ask', 'larger_ask',
        'changed_channel', 'corrected_fact', 'deferred_to_buyer_timing', 'style_only']),
    evidence                    jsonb NOT NULL CHECK (CASE WHEN jsonb_typeof(evidence -> 'evidence_refs') = 'array'
                                    THEN jsonb_array_length(evidence -> 'evidence_refs') >= 1 ELSE false END),
    human_verdict               text NOT NULL DEFAULT 'pending' CHECK (human_verdict IN ('pending', 'confirmed', 'corrected')),
    corrected_statement         text CHECK (length(corrected_statement) BETWEEN 1 AND 1500),
    human_note                  text CHECK (length(human_note) <= 2000),
    verdict_surface             text CHECK (verdict_surface IN ('web', 'slack', 'mcp', 'api')),
    verdict_actor_label         text CHECK (length(verdict_actor_label) <= 200),
    verdict_at                  timestamptz,
    model                       text,
    generated_at                timestamptz NOT NULL,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT judgment_inferences_same_choice FOREIGN KEY
        (human_strategy_decision_id, decision_episode_id, agent_preference, human_choice)
        REFERENCES human_strategy_decisions (id, decision_episode_id, original_agent_preference, selected_candidate_id)
        ON DELETE RESTRICT,
    CONSTRAINT judgment_inferences_agreement_matches CHECK ((agreement = 'agreed') = (agent_preference = human_choice)),
    CONSTRAINT judgment_inferences_pending_is_open CHECK (human_verdict <> 'pending' OR (
        verdict_at IS NULL AND corrected_statement IS NULL)),
    CONSTRAINT judgment_inferences_answer_is_attributed CHECK (human_verdict = 'pending' OR (
        verdict_at IS NOT NULL AND verdict_surface IS NOT NULL)),
    CONSTRAINT judgment_inferences_correction_has_statement CHECK (
        (human_verdict = 'corrected') = (corrected_statement IS NOT NULL))
);
-- +goose Down
DROP TABLE judgment_inferences;
DROP TRIGGER human_strategy_decisions_rules ON human_strategy_decisions;
DROP FUNCTION human_strategy_decisions_check();
DROP TABLE human_strategy_decisions;
DROP TRIGGER strategy_candidates_size_check ON strategy_candidates;
DROP TRIGGER strategy_sets_size_check ON strategy_sets;
DROP TABLE strategy_candidates;
DROP FUNCTION strategy_sets_check_size();
DROP TABLE strategy_sets;
DROP TABLE eval_bundles;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION decision_episodes_check_action() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    d text;
    r uuid;
BEGIN
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
DROP INDEX decision_episodes_account_change_idx;
ALTER TABLE decision_episodes
    DROP CONSTRAINT decision_episodes_id_run_uniq,
    DROP CONSTRAINT decision_episodes_replay_has_change,
    DROP CONSTRAINT decision_episodes_status_fields,
    DROP COLUMN learning_scope,
    DROP COLUMN business_intelligence_update_id,
    DROP COLUMN account_change_id,
    DROP COLUMN held_out_event_id,
    DROP COLUMN status,
    ALTER COLUMN final_draft_index SET NOT NULL,
    ALTER COLUMN human_action SET NOT NULL,
    ALTER COLUMN human_decision_id SET NOT NULL;
DROP TABLE business_intelligence_updates;
DROP TABLE account_changes;
DROP TABLE demo_manifests;
ALTER TABLE state_diffs DROP CONSTRAINT state_diffs_id_account_material_uniq;
ALTER TABLE agent_run_drafts
    DROP CONSTRAINT agent_run_drafts_source_index,
    DROP CONSTRAINT agent_run_drafts_source_check;
ALTER TABLE agent_run_drafts
    ADD CONSTRAINT agent_run_drafts_source_check CHECK (source IN ('account_agent', 'revision_planner')),
    ADD CONSTRAINT agent_run_drafts_first_from_agent CHECK ((draft_index = 1) = (source = 'account_agent'));
