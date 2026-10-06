"""Spec-derived seed gold for the HAR-97 L4 knowledge evals (WP20, HAR-118).

Every case is built from an example written in HAR-97 itself (L4 'Knowledge extraction fidelity' and
'Knowledge scope / exception quality', §7 learning attached to a transition) with neutral people (champion,
new stakeholder) and no company: no invented account fixtures. The expected labels are single-author and the
judge responses below are hand-written cassette content, so the semantic agreement they show is circular and is
NOT reported as a measurement; CRMArena-based episodes and a recorded judge replace them.
"""
from __future__ import annotations

from dataclasses import dataclass

from ghost_worker.knowledge_evals import Condition, Correction, InferredKnowledge, KnowledgeException, ReferenceLesson


def c(field: str, op: str, value: object = None) -> Condition:
    return Condition(field=field, op=op, value=value)


def x(description: str, *conditions: Condition) -> KnowledgeException:
    return KnowledgeException(description=description, conditions=conditions)


CHAMPION_SCOPE = (c("motion", "eq", "expansion"), c("diff.new_stakeholder_entered", "exists"), c("champion", "exists"))
CHAMPION_EXCEPTIONS = (
    x("Champion explicitly delegated ownership", c("champion_status", "eq", "delegated")),
    x("Champion left the company", c("champion_status", "eq", "departed")),
    x("Champion no longer involved", c("champion_status", "eq", "inactive")),
)
CHAMPION = ReferenceLesson(
    reusable=True, scope=CHAMPION_SCOPE, exceptions=CHAMPION_EXCEPTIONS,
    principle="Preserve an active champion's involvement when a new technical stakeholder enters an expansion "
              "motion, unless ownership has explicitly transferred.")
CHAMPION_CORRECTION = Correction(
    account_state_summary="Expansion motion. The champion is active. A new technical stakeholder joined this week.",
    agent_proposal="Email the new technical stakeholder directly to hand off the evaluation.",
    eval_results="champion_continuity: pass (no eval flagged the handoff).",
    human_edit="Removed the direct handoff; replaced it with a three-way meeting including the champion.",
    final_artifact="Invite to a three-way meeting: champion, new technical stakeholder, rep.")

REORG_SCOPE = (c("relationship_state", "eq", "REORG"), c("transition.to_state", "eq", "EXPANSION"),
               c("transition.status", "eq", "CANDIDATE"))
REORG = ReferenceLesson(
    reusable=True, scope=REORG_SCOPE,
    principle="When ownership is still unresolved during a possible expansion transition after a reorg, rebuild "
              "the relationship before making an expansion ask.")
REORG_CORRECTION = Correction(
    account_state_summary="REORG -> EXPANSION transition is CANDIDATE: new business unit interested, relationship "
                          "owner not stabilized, economic buyer unknown.",
    agent_proposal="Push an expansion meeting with a pricing discussion.",
    eval_results="cta_calibration: fail (premature CTA).",
    human_edit="Changed the action to a low-pressure relationship-rebuild follow-up confirming who owns the program.",
    final_artifact="Short note asking who now owns the program, no meeting request.")

TYPO = ReferenceLesson(reusable=False)
TYPO_CORRECTION = Correction(
    account_state_summary="Expansion motion; champion active.",
    agent_proposal="Follow-up email that misspells the champion's surname.",
    human_edit="Fixed the spelling of the surname; nothing else changed.")


def learned(title: str, summary: str, scope: tuple[Condition, ...],
            exceptions: tuple[KnowledgeException, ...] = ()) -> InferredKnowledge:
    return InferredKnowledge(title=title, situation_signature=scope[:1] or scope,
                             applicability_conditions=scope[1:], exceptions=exceptions,
                             guidance={"summary": summary, "do": [], "dont": []})


KEEP = "Keep the active champion in the next interaction when a new stakeholder enters an expansion, unless " \
       "the champion handed ownership over."
REBUILD = "During a candidate expansion after a reorg, confirm the new owner before any expansion ask."


@dataclass(frozen=True)
class LessonCase:
    id: str
    correction: Correction
    reference: ReferenceLesson
    inferred: InferredKnowledge | None
    judge_label: str | None  # hand-written cassette response; None when the judge must not be called
    reusable: str
    scope: str | None
    exceptions: str | None
    fidelity: str
    scope_quality: str


CASES = (
    LessonCase("champion_correct", CHAMPION_CORRECTION, CHAMPION,
               learned("Preserve the champion", KEEP, CHAMPION_SCOPE, CHAMPION_EXCEPTIONS),
               "SAME_PRINCIPLE", "PRINCIPLE_DETECTED", "SCOPE_MATCH", "EXCEPTIONS_CAPTURED", "pass", "pass"),
    LessonCase("always_cc_the_champion", CHAMPION_CORRECTION, CHAMPION,
               learned("Always CC the champion", "Always cc the champion on every email.", (c("champion", "exists"),)),
               "PARTIAL_PRINCIPLE", "PRINCIPLE_DETECTED", "OVER_GENERALISED", "EXCEPTIONS_MISSED", "fail", "fail"),
    LessonCase("champion_departure_exception_dropped", CHAMPION_CORRECTION, CHAMPION,
               learned("Preserve the champion", KEEP, CHAMPION_SCOPE, CHAMPION_EXCEPTIONS[:1]),
               "SAME_PRINCIPLE", "PRINCIPLE_DETECTED", "SCOPE_MATCH", "EXCEPTIONS_PARTIAL", "pass", "fail"),
    LessonCase("champion_exceptions_unreachable", CHAMPION_CORRECTION, CHAMPION,
               # The K17 v1 defect: 'champion_status eq active' in the scope makes every exception unreachable.
               learned("Preserve the champion", KEEP, CHAMPION_SCOPE + (c("champion_status", "eq", "active"),),
                       CHAMPION_EXCEPTIONS),
               "SAME_PRINCIPLE", "PRINCIPLE_DETECTED", "OVER_NARROW", "EXCEPTIONS_MISSED", "pass", "fail"),
    LessonCase("champion_scoped_to_one_stage", CHAMPION_CORRECTION, CHAMPION,
               learned("Preserve the champion", KEEP, CHAMPION_SCOPE + (c("stage", "eq", "Technical evaluation"),),
                       CHAMPION_EXCEPTIONS),
               "SAME_PRINCIPLE", "PRINCIPLE_DETECTED", "OVER_NARROW", "EXCEPTIONS_CAPTURED", "pass", "fail"),
    LessonCase("champion_wrong_principle", CHAMPION_CORRECTION, CHAMPION,
               learned("Hand off to the new stakeholder", "Move the thread to the new stakeholder and drop the champion.",
                       CHAMPION_SCOPE, CHAMPION_EXCEPTIONS),
               "DIFFERENT_PRINCIPLE", "PRINCIPLE_DETECTED", "SCOPE_MATCH", "EXCEPTIONS_CAPTURED", "fail", "pass"),
    LessonCase("champion_lesson_missed", CHAMPION_CORRECTION, CHAMPION, None,
               None, "PRINCIPLE_MISSED", None, None, "fail", "fail"),
    LessonCase("reorg_candidate_correct", REORG_CORRECTION, REORG,
               learned("Rebuild before expanding", REBUILD, REORG_SCOPE),
               "SAME_PRINCIPLE", "PRINCIPLE_DETECTED", "SCOPE_MATCH", "NO_EXCEPTIONS_EXPECTED", "pass", "pass"),
    LessonCase("reorg_lesson_without_transition_scope", REORG_CORRECTION, REORG,
               learned("Rebuild before expanding", REBUILD, (c("motion", "eq", "expansion"),)),
               "SAME_PRINCIPLE", "PRINCIPLE_DETECTED", "WRONG_SCOPE", "NO_EXCEPTIONS_EXPECTED", "fail", "fail"),
    LessonCase("reorg_lesson_kept_after_confirmation", REORG_CORRECTION, REORG,
               learned("Rebuild before expanding", REBUILD,
                       REORG_SCOPE[:2] + (c("transition.status", "in", ["CANDIDATE", "CONFIRMED"]),)),
               "SAME_PRINCIPLE", "PRINCIPLE_DETECTED", "OVER_GENERALISED", "NO_EXCEPTIONS_EXPECTED", "fail", "fail"),
    LessonCase("typo_fix_abstained", TYPO_CORRECTION, TYPO, None,
               None, "CORRECT_ABSTENTION", None, None, "pass", "pass"),
    LessonCase("typo_fix_turned_into_a_rule", TYPO_CORRECTION, TYPO,
               learned("Double-check names", "Always double-check the spelling of names.", (c("champion", "exists"),)),
               None, "SPURIOUS_PRINCIPLE", None, None, "fail", "fail"),
)

JUDGE_RATIONALES = {
    "SAME_PRINCIPLE": "Same action for the same reason as the reference.",
    "PARTIAL_PRINCIPLE": "Right instinct, but a blanket rule that ignores when the champion should step back.",
    "DIFFERENT_PRINCIPLE": "Leads to the opposite action: it drops the champion the reference keeps involved.",
}
