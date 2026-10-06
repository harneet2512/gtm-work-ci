"""Neutral judge situations with hand-authored judge answers: the source of cassettes/judges/unit/ (seeded by
scripts/seed_judge_cassettes.py) and of the replay test. No invented companies."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from judge_doubles import ACT_1, RUBRICS, answer, context

from ghost_worker.judges import JudgeContext

UNIT_RUN_ID = "0d000000-0000-4000-8000-0000000000bb"
WAIT_ACTION = {"proposed_action_type": "wait", "recipients": [], "evidence_refs": [],
               "finished_artifact": {"channel": "none", "subject": None, "body": "", "attachments": []},
               "wait_until": "2026-10-05T09:00:00Z"}
NO_SIGN_ACTION = {"finished_artifact": {"channel": "email", "subject": "Security questionnaire",
                                        "body": "Hi, the answers are attached. Happy to help once your team has read them.",
                                        "attachments": ["answers.pdf"]}}


@dataclass(frozen=True)
class Situation:
    name: str
    context: JudgeContext
    answers: dict[str, dict[str, Any]] = field(default_factory=dict)  # eval_type -> scripted answer
    expected: dict[str, tuple[str, str | None, bool]] = field(default_factory=dict)  # verdict, label, blocking


def _too_early() -> Situation:
    br, cta = RUBRICS["buyer_readiness"], RUBRICS["cta_calibration"]
    return Situation(
        name="signature_push_before_security_review",
        context=context(),
        answers={
            "buyer_readiness": answer(br, verdict="fail", label="TOO_EARLY", diagnostics=["too_early"], blocks=True,
                                      suggested_correction="Send the answers and leave the timing to the reviewer."),
            "cta_calibration": answer(cta, verdict="fail", label="TOO_STRONG", diagnostics=["ask_too_strong"]),
            "grounding": answer(RUBRICS["grounding"]),
        },
        expected={"buyer_readiness": ("fail", "TOO_EARLY", True), "cta_calibration": ("fail", "TOO_STRONG", False),
                  "grounding": ("pass", None, False)},
    )


def _answers_only() -> Situation:
    return Situation(
        name="answers_sent_no_ask",
        context=context(action=NO_SIGN_ACTION),
        answers={"buyer_readiness": answer(RUBRICS["buyer_readiness"]),
                 "cta_calibration": answer(RUBRICS["cta_calibration"])},
        expected={"buyer_readiness": ("pass", "READY", False), "cta_calibration": ("pass", "APPROPRIATE", False)},
    )


def _wait() -> Situation:
    nq, tc = RUBRICS["next_action_quality"], RUBRICS["timing_cadence"]
    return Situation(
        name="wait_for_the_review",
        context=context(action=WAIT_ACTION, commitments=[]),
        answers={"next_action_quality": answer(nq, label="WAIT", evidence_refs=[{"activity_id": ACT_1, "quote": None}]),
                 "timing_cadence": answer(tc, verdict="fail", label="LATE_OR_MISSED_WINDOW", blocks=False)},
        expected={"next_action_quality": ("pass", "WAIT", False),
                  "timing_cadence": ("fail", "LATE_OR_MISSED_WINDOW", False)},
    )


SITUATIONS: dict[str, Situation] = {s.name: s for s in (_too_early(), _answers_only(), _wait())}
