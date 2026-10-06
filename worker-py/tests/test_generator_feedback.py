"""HAR-119 generator_feedback: the worker model mirrors worker.yaml, the planner prompt renders the corrections
as data (never evidence), and an absent/empty list prompts byte-identically to before the field existed —
recorded cassettes keep matching."""
from __future__ import annotations

import pytest
from pydantic import ValidationError

import strategy_doubles as sd
from ghost_worker.models.strategies import GeneratorFeedback, StrategiesRequest
from ghost_worker.strategies.prompt import build_planner_user_prompt, render_feedback

FEEDBACK = {
    "eval_result_id": "0e000000-0000-4000-8000-0000000000e5",
    "eval_type": "cta_calibration",
    "instruction": "The rep replaced a hard ask with a softer check-in; keep the CTA proportional to readiness.",
}


def request(feedback=None) -> StrategiesRequest:
    body = sd.strategies_request()
    if feedback is not None:
        body["generator_feedback"] = feedback
    return StrategiesRequest.model_validate(body)


def test_model_defaults_to_empty_and_parses_items() -> None:
    assert request().generator_feedback == ()
    parsed = request([FEEDBACK])
    assert len(parsed.generator_feedback) == 1
    item = parsed.generator_feedback[0]
    assert item.eval_type == "cta_calibration"
    assert str(item.eval_result_id) == FEEDBACK["eval_result_id"]
    assert item.instruction == FEEDBACK["instruction"]


def test_model_validates_items() -> None:
    with pytest.raises(ValidationError):
        request([{"eval_result_id": FEEDBACK["eval_result_id"], "eval_type": "cta_calibration"}])
    with pytest.raises(ValidationError):
        request([dict(FEEDBACK, instruction="")])
    with pytest.raises(ValidationError):
        request([dict(FEEDBACK, eval_type="not_an_eval_axis")])
    with pytest.raises(ValidationError):
        request([FEEDBACK] * 21)


def test_render_feedback_is_empty_without_corrections() -> None:
    assert render_feedback(()) == ""
    assert render_feedback(request().generator_feedback) == ""


def test_prompt_is_byte_identical_without_feedback() -> None:
    # An absent field and an empty list both produce the pre-HAR-119 prompt: cassette keys are stable.
    assert build_planner_user_prompt(request()) == build_planner_user_prompt(request([]))


def test_prompt_renders_feedback_as_correction_data() -> None:
    prompt = build_planner_user_prompt(request([FEEDBACK]))
    assert "Human corrections" in prompt
    assert "cta_calibration" in prompt
    assert FEEDBACK["instruction"] in prompt
    assert "not evidence" in prompt  # corrections are never cited as facts or ids
    # The feedback block sits between decision guidance and the transition/footer it precedes.
    assert prompt.rstrip().endswith("Pull what you need, then propose the three strategies.")
    assert "never cite them as facts or ids" in prompt


def test_prompt_lists_each_correction() -> None:
    second = dict(FEEDBACK, eval_result_id="0e000000-0000-4000-8000-0000000000e6",
                  eval_type="channel_appropriateness", instruction="The buyer lives in Slack.")
    prompt = build_planner_user_prompt(request([FEEDBACK, second]))
    assert prompt.count("- ") >= 2
    assert "- cta_calibration: " in prompt
    assert "- channel_appropriateness: " in prompt
