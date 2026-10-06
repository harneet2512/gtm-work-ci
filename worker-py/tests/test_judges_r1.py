"""WP-A rule R1 (decision-evals audit): no evidence, no pass. A model answer that says pass but cites no state field,
activity, evidence quote or knowledge is coerced to unknown by interpret(); every result names the object it judged and
the trace span it attaches to; a model abstain and a judge that did not answer are both unknown."""
from __future__ import annotations

from datetime import datetime, timezone

import pytest
from judge_doubles import ACT_1, CATALOG, RUBRICS, RUN_ID, answer, context, scripted
from test_contracts import validator

from ghost_worker.judges import JudgeRun, judge

EVAL_RESULT = validator("eval_result")
FIXED = datetime(2026, 10, 2, 12, 0, tzinfo=timezone.utc)
CANDIDATE_ID = "0d000000-0000-4000-8000-0000000000c1"
NO_REFS = {"state_refs": [], "evidence_refs": [], "knowledge_refs": []}


def run(name: str, content: dict, run_: JudgeRun | None = None):
    provider = scripted((name, content))
    return judge(provider, RUBRICS[name], CATALOG, context(), run_ or JudgeRun(agent_run_id=RUN_ID), clock=lambda: FIXED)


def test_a_pass_that_cites_nothing_is_unknown_not_a_pass() -> None:
    outcome = run("grounding", answer(RUBRICS["grounding"], verdict="pass", **NO_REFS))
    result = outcome.eval_result()
    assert outcome.ok and result["verdict"] == "unknown" and result["blocking"] is False
    assert "unknown, not a pass" in result["reason"]
    assert not list(EVAL_RESULT.iter_errors(result))


def test_a_coerced_pass_drops_its_label_because_it_asserts_nothing() -> None:
    outcome = run("buyer_readiness", answer(RUBRICS["buyer_readiness"], verdict="pass", **NO_REFS))
    assert outcome.result["verdict"] == "unknown" and outcome.result["label"] is None


@pytest.mark.parametrize("cited", [
    {"state_refs": ["next_milestone"], "evidence_refs": [], "knowledge_refs": []},
    {"state_refs": [], "evidence_refs": [{"activity_id": ACT_1, "quote": None}], "knowledge_refs": []},
])
def test_a_pass_with_any_one_kind_of_evidence_stays_a_pass(cited: dict) -> None:
    outcome = run("grounding", answer(RUBRICS["grounding"], verdict="pass", **cited))
    assert outcome.result["verdict"] == "pass"


def test_a_pass_whose_only_state_ref_is_not_in_the_context_is_unknown() -> None:
    """Refs are checked against the judge's own context first: a made-up field is not evidence."""
    outcome = run("grounding", answer(RUBRICS["grounding"], verdict="pass", state_refs=["made_up_field"],
                                      evidence_refs=[], knowledge_refs=[]))
    assert outcome.result["verdict"] == "unknown"


@pytest.mark.parametrize("verdict", ["warn", "fail"])
def test_only_a_pass_is_coerced(verdict: str) -> None:
    outcome = run("grounding", answer(RUBRICS["grounding"], verdict=verdict, **NO_REFS))
    assert outcome.result["verdict"] == verdict


def test_the_models_abstain_is_recorded_as_unknown() -> None:
    outcome = run("buyer_readiness", answer(RUBRICS["buyer_readiness"], verdict="abstain", label=None))
    assert outcome.result["verdict"] == "unknown" and outcome.result["label"] is None
    assert not list(EVAL_RESULT.iter_errors(outcome.eval_result()))


def test_a_result_names_what_it_judged_and_where() -> None:
    outcome = run("grounding", answer(RUBRICS["grounding"]), JudgeRun(agent_run_id=RUN_ID, candidate_id=CANDIDATE_ID))
    result = outcome.eval_result()
    assert result["judged_object"] == {"type": "StrategyCandidate", "id": CANDIDATE_ID}
    assert result["span_id"] == f"candidates:{RUN_ID}"
    assert not list(EVAL_RESULT.iter_errors(result))


def test_without_a_candidate_the_run_is_the_judged_object() -> None:
    result = run("grounding", answer(RUBRICS["grounding"])).eval_result()
    assert result["judged_object"] == {"type": "AgentRun", "id": RUN_ID}

