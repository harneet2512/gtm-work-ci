-- +goose Up
-- HAR-129 Cliff Message 3 / HAR-137: "Don't learn this". The human can answer Ghost's judgment inference with
-- an explicit no-learning verdict: nothing is learned from the episode (no candidate criterion or knowledge is
-- seeded from it). The verdict vocabulary gains 'no_learning' on the latest answer (judgment_inferences) and
-- on the append-only history (judgment_verdicts). A no-learning answer, like confirmed, carries no corrected
-- statement; the existing correction_has_statement and answer_is_attributed checks already say so.
ALTER TABLE judgment_inferences DROP CONSTRAINT judgment_inferences_human_verdict_check;
ALTER TABLE judgment_inferences ADD CONSTRAINT judgment_inferences_human_verdict_check
    CHECK (human_verdict IN ('pending', 'confirmed', 'corrected', 'no_learning'));

ALTER TABLE judgment_verdicts DROP CONSTRAINT judgment_verdicts_verdict_check;
ALTER TABLE judgment_verdicts ADD CONSTRAINT judgment_verdicts_verdict_check
    CHECK (verdict IN ('confirmed', 'corrected', 'no_learning'));

-- A no-learning answer withdraws the episode's queued generator feedback rather than deleting it: the row
-- stays as the audit trail of what was queued, when and why it was withdrawn, and the strategies call skips
-- withdrawn rows.
ALTER TABLE generator_feedback
    ADD COLUMN withdrawn_at     timestamptz,
    ADD COLUMN withdrawn_reason text,
    ADD CONSTRAINT generator_feedback_withdrawn_check
        CHECK ((withdrawn_at IS NULL) = (withdrawn_reason IS NULL)
               AND (withdrawn_reason IS NULL OR length(btrim(withdrawn_reason)) > 0));

-- +goose Down
-- Withdrawn feedback would become offerable again without the columns, so it is removed with them.
DELETE FROM generator_feedback WHERE withdrawn_at IS NOT NULL;
ALTER TABLE generator_feedback DROP CONSTRAINT generator_feedback_withdrawn_check,
    DROP COLUMN withdrawn_reason, DROP COLUMN withdrawn_at;

-- A no-learning answer has no earlier vocabulary to fall back to: its history rows are removed and the
-- inference is reopened (pending) so the narrower checks hold. judgment_verdicts is append-only by trigger, so
-- the trigger is paused for this one rewrite.
ALTER TABLE judgment_verdicts DISABLE TRIGGER judgment_verdicts_append_only;
DELETE FROM judgment_verdicts WHERE verdict = 'no_learning';
ALTER TABLE judgment_verdicts ENABLE TRIGGER judgment_verdicts_append_only;
UPDATE judgment_inferences SET human_verdict = 'pending', verdict_at = NULL, verdict_surface = NULL,
    verdict_actor_label = NULL WHERE human_verdict = 'no_learning';
ALTER TABLE judgment_verdicts DROP CONSTRAINT judgment_verdicts_verdict_check;
ALTER TABLE judgment_verdicts ADD CONSTRAINT judgment_verdicts_verdict_check
    CHECK (verdict IN ('confirmed', 'corrected'));
ALTER TABLE judgment_inferences DROP CONSTRAINT judgment_inferences_human_verdict_check;
ALTER TABLE judgment_inferences ADD CONSTRAINT judgment_inferences_human_verdict_check
    CHECK (human_verdict IN ('pending', 'confirmed', 'corrected'));
