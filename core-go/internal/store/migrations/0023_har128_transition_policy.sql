-- +goose Up
-- HAR-117 / HAR-128: the run orchestrator's records. A strategy candidate carries its decision class and the five
-- questions it answers; an eval bundle records the suite the router selected and the transition policy verdict; a
-- decision episode stores the StateTransition the agent read (never rewritten by the agent).
-- (Numbered 0023: main's highest + 1 at authoring; renumber on conflict.)
ALTER TABLE strategy_candidates
    ADD COLUMN action_class text NOT NULL DEFAULT 'UNKNOWN',
    ADD COLUMN five_questions jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- strategy_candidate.v1.json selected_eval_suite: the suite core routed this candidate to (status + state + action)
    ADD COLUMN selected_eval_suite text CHECK (selected_eval_suite IS NULL OR selected_eval_suite ~ '^[a-z][a-z0-9_]*$');
UPDATE strategy_candidates SET action_class = CASE action_type
    WHEN 'send_email' THEN 'REPLY' WHEN 'schedule_meeting' THEN 'MEETING' WHEN 'share_document' THEN 'SHARE_DOCUMENT'
    WHEN 'internal_note' THEN 'INTERNAL_TASK' WHEN 'wait' THEN 'WAIT' ELSE 'NO_ACTION' END,
    five_questions = jsonb_build_object(
        'what_changed', 'not recorded', 'why_state_changed', 'not recorded', 'what_remains_unknown', 'not recorded',
        'prior_knowledge_applies', 'not recorded', 'why_next_action', 'not recorded');
ALTER TABLE strategy_candidates
    ALTER COLUMN action_class DROP DEFAULT,
    ALTER COLUMN five_questions DROP DEFAULT,
    ADD CONSTRAINT strategy_candidates_action_class_check CHECK (action_class IN (
        'REPLY', 'MEETING', 'SHARE_DOCUMENT', 'INTERNAL_TASK', 'WAIT', 'NO_ACTION', 'ASK_RESEARCH', 'EXPANSION_MOTION',
        'CRM_UPDATE', 'UNKNOWN', 'HUMAN_REVIEW')),
    -- contracts/schemas/strategy_candidate.v1.json allOf: the decision class is carried out by these tool actions
    ADD CONSTRAINT strategy_candidates_class_action CHECK (
        (action_class = 'REPLY' AND action_type = 'send_email')
        OR (action_class = 'MEETING' AND action_type = 'schedule_meeting')
        OR (action_class = 'SHARE_DOCUMENT' AND action_type = 'share_document')
        OR (action_class IN ('INTERNAL_TASK', 'ASK_RESEARCH', 'CRM_UPDATE', 'HUMAN_REVIEW') AND action_type = 'internal_note')
        OR (action_class = 'WAIT' AND action_type = 'wait')
        OR (action_class IN ('NO_ACTION', 'UNKNOWN') AND action_type = 'no_action')
        OR (action_class = 'EXPANSION_MOTION' AND action_type IN ('send_email', 'schedule_meeting', 'share_document'))),
    ADD CONSTRAINT strategy_candidates_five_questions CHECK (
        jsonb_typeof(five_questions) = 'object'
        AND five_questions ?& ARRAY['what_changed', 'why_state_changed', 'what_remains_unknown', 'prior_knowledge_applies', 'why_next_action']
        AND (five_questions - ARRAY['what_changed', 'why_state_changed', 'what_remains_unknown', 'prior_knowledge_applies', 'why_next_action']) = '{}'::jsonb
        AND NOT jsonb_path_exists(five_questions, '$.* ? (@.type() != "string" || @ == "")'));

ALTER TABLE eval_bundles
    ADD COLUMN selected_eval_suite text CHECK (selected_eval_suite IS NULL OR selected_eval_suite ~ '^[a-z][a-z0-9_]*$'),
    -- eval_bundle.v1.json candidate_policy: the reasons are the closed enum, an allowed verdict has none, and a restricted
    -- one has at least one and requires human review.
    ADD COLUMN candidate_policy jsonb CHECK (candidate_policy IS NULL OR coalesce(jsonb_typeof(candidate_policy) = 'object'
        AND candidate_policy ->> 'status' IN ('allowed', 'restricted')
        AND candidate_policy ->> 'transition_status' IN ('CONFIRMED', 'CANDIDATE', 'UNRESOLVED', 'REJECTED')
        AND jsonb_typeof(candidate_policy -> 'reasons') = 'array'
        AND NOT jsonb_path_exists(candidate_policy -> 'reasons', '$[*] ? (@.type() != "string" || (@ != "expansion_motion"
            && @ != "pricing_push" && @ != "commercial_escalation" && @ != "broad_outreach" && @ != "expansion_label_mismatch"))')
        AND ((candidate_policy ->> 'status' = 'allowed' AND jsonb_array_length(candidate_policy -> 'reasons') = 0)
          OR (candidate_policy ->> 'status' = 'restricted' AND jsonb_array_length(candidate_policy -> 'reasons') >= 1
              AND candidate_policy -> 'requires_human_review' = 'true'::jsonb)), false));

-- strategy_set.v1.json no_acceptable_candidate: every candidate blocked or restricted; Ghost recommends none.
ALTER TABLE strategy_sets
    ADD COLUMN no_acceptable_candidate boolean NOT NULL DEFAULT false;

ALTER TABLE decision_episodes
    ADD COLUMN state_transition jsonb CHECK (state_transition IS NULL OR jsonb_typeof(state_transition) = 'object'),
    ADD COLUMN transition_status text CHECK (transition_status IS NULL OR transition_status IN ('CONFIRMED', 'CANDIDATE', 'UNRESOLVED', 'REJECTED')),
    ADD COLUMN supporting_evidence jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(supporting_evidence) = 'array'),
    ADD COLUMN missing_evidence jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(missing_evidence) = 'array'),
    ADD COLUMN selected_eval_suite text CHECK (selected_eval_suite IS NULL OR selected_eval_suite ~ '^[a-z][a-z0-9_]*$'),
    ADD CONSTRAINT decision_episodes_transition_fields CHECK (
        (state_transition IS NULL AND transition_status IS NULL)
        OR (state_transition IS NOT NULL AND transition_status = state_transition ->> 'status'));

-- +goose Down
ALTER TABLE strategy_sets DROP COLUMN no_acceptable_candidate;
ALTER TABLE decision_episodes
    DROP CONSTRAINT decision_episodes_transition_fields,
    DROP COLUMN selected_eval_suite,
    DROP COLUMN missing_evidence,
    DROP COLUMN supporting_evidence,
    DROP COLUMN transition_status,
    DROP COLUMN state_transition;
ALTER TABLE eval_bundles DROP COLUMN candidate_policy, DROP COLUMN selected_eval_suite;
ALTER TABLE strategy_candidates
    DROP CONSTRAINT strategy_candidates_five_questions,
    DROP CONSTRAINT strategy_candidates_class_action,
    DROP CONSTRAINT strategy_candidates_action_class_check,
    DROP COLUMN selected_eval_suite,
    DROP COLUMN five_questions,
    DROP COLUMN action_class;
