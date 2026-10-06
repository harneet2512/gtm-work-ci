"""Unit tests of the knowledge-eval building blocks (minimal neutral conditions, no fixtures)."""
from __future__ import annotations

import pytest
from pydantic import ValidationError

from ghost_worker.errors import CassetteNotFoundError, InvalidModelOutputError
from ghost_worker.knowledge_evals import (
    Condition, Correction, InferredKnowledge, PrincipleJudge, ReferenceLesson, exception_quality, scope_quality,
)
from ghost_worker.knowledge_evals.conditions import ENGAGED, disjoint, equivalent, narrows, norm
from ghost_worker.llm.fake_provider import FakeProvider
from ghost_worker.llm.provider import LLMResult
from knowledge_lessons import CHAMPION, CHAMPION_CORRECTION, c, learned, x
from test_contracts import SCHEMAS, load_json


@pytest.mark.parametrize(("a", "b", "want"), [
    (c("stage", "eq", "Technical  Evaluation"), c("stage", "in", ["technical evaluation", "discovery"]), True),
    (c("stage", "in", ["a", "b"]), c("stage", "eq", "a"), False),
    (c("stage", "eq", "a"), c("stage", "neq", "b"), True),
    (c("stage", "eq", "b"), c("stage", "neq", "b"), False),
    (c("stage", "neq", "b"), c("stage", "neq", "b"), True),
    (c("stage", "eq", "a"), c("stage", "exists"), True),
    (c("blockers", "contains", "x"), c("blockers", "exists"), True),
    (c("stage", "not_exists"), c("stage", "exists"), False),
    (c("stage", "not_exists"), c("stage", "not_exists"), True),
    (c("stage", "is_unknown"), c("stage", "is_unknown"), True),
    (c("blockers", "contains", {"text": "Security", "status": "open"}), c("blockers", "contains", {"status": "open", "text": "security"}), True),
    (c("stage", "eq", "a"), c("health", "eq", "a"), False),
    (c("stage", "is_unknown"), c("stage", "not_exists"), True),
    (c("blockers", "contains", "security review"), c("blockers", "contains", "Security"), True),
    (c("blockers", "contains", "security"), c("blockers", "contains", "security review"), False),
    (c("blockers", "contains", {"text": "budget", "status": ["resolved"]}), c("blockers", "contains", "budget"), True),
    (c("blockers", "contains", "budget"), c("blockers", "contains", {"text": "budget", "status": "resolved"}), False),
    (c("blockers", "contains", {"text": "budget", "status": "open"}), c("blockers", "contains", {"status": ["open", "overdue"]}), True),
    (c("health", "eq", 1), c("health", "in", [1.0, 2]), True),
    (c("buying_group.champion", "eq", "departed"), c("buying_group.champion", "exists"), False),
    (c("buying_group.champion", "in", ["active", "new"]), c("buying_group.champion", "exists"), True),
])
def test_narrows(a: Condition, b: Condition, want: bool) -> None:
    assert narrows(a, b) is want


@pytest.mark.parametrize(("a", "b", "want"), [
    (c("s", "eq", "a"), c("s", "in", ["b", "c"]), True),
    (c("s", "eq", "a"), c("s", "in", ["a", "c"]), False),
    (c("s", "exists"), c("s", "not_exists"), True),
    (c("s", "eq", "a"), c("s", "not_exists"), True),
    (c("s", "eq", "a"), c("s", "neq", "a"), True),
    (c("s", "neq", "a"), c("s", "eq", "a"), True),
    (c("s", "neq", "a"), c("s", "eq", "b"), False),
    (c("s", "contains", "a"), c("s", "exists"), False),
    (c("s", "eq", "a"), c("t", "eq", "b"), False),
    (c("buying_group.champion", "eq", "active"), c("buying_group.champion", "eq", "departed"), False),
    (c("buying_group.champion", "eq", "departed"), c("buying_group.champion", "not_exists"), False),
    (c("buying_group.champion", "eq", "active"), c("buying_group.champion", "not_exists"), True),
    (c("buying_group.champion", "exists"), c("buying_group.champion", "not_exists"), True),
])
def test_disjoint(a: Condition, b: Condition, want: bool) -> None:
    assert disjoint(a, b) is want


def test_equivalence_and_normalisation() -> None:
    assert norm("  Expansion. ") == "expansion" and norm(3) == norm(3.0) and norm(True) == "true" and norm(["B", "a"]) == norm(("a", "b"))
    assert equivalent((c("s", "eq", "a"),), (c("s", "in", ["A"]),))
    assert not equivalent((c("s", "eq", "a"),), (c("s", "eq", "a"), c("t", "exists")))


def test_scope_ratios() -> None:
    r = scope_quality((c("motion", "eq", "expansion"), c("stage", "eq", "x")), (c("motion", "eq", "expansion"), c("champion", "exists")))
    assert r.label == "WRONG_SCOPE" and r.condition_recall == 0.5 and r.condition_precision == 0.5
    assert not r.transition_scope_missing
    empty = scope_quality((c("motion", "exists"),), ())
    assert empty.condition_recall == 1.0 and empty.label == "OVER_NARROW"


def test_exception_quality_extra_and_no_reference() -> None:
    r = exception_quality((x("extra", c("health", "eq", "red")),), (), (c("motion", "eq", "expansion"),))
    assert r.label == "NO_EXCEPTIONS_EXPECTED" and r.recall is None and r.extra == ("extra",)


@pytest.mark.parametrize("bad", [
    {"field": "motion", "op": "eq"},
    {"field": "motion", "op": "exists", "value": True},
    {"field": "motion", "op": "in", "value": []},
    {"field": "motion", "op": "eq", "value": ["a"]},
    {"field": "blockers", "op": "contains", "value": " "},
    {"field": "Motion", "op": "eq", "value": "a"},
    {"field": "motion", "op": "gt", "value": 1},
])
def test_malformed_conditions_fail_loudly(bad: dict) -> None:
    with pytest.raises(ValidationError):
        Condition.model_validate(bad)


def test_reference_lessons_are_consistent() -> None:
    with pytest.raises(ValidationError):
        ReferenceLesson(reusable=True, principle="p")
    with pytest.raises(ValidationError):
        ReferenceLesson(reusable=False, principle="p")
    assert c("stage", "exists").render() == "stage exists"


class StubProvider:
    def __init__(self, content: dict) -> None:
        self.content = content

    def complete_json(self, **_: object) -> LLMResult:
        return LLMResult(content=self.content, model="stub")


def _inferred() -> InferredKnowledge:
    return learned("t", "s", (c("motion", "eq", "expansion"),))


@pytest.mark.parametrize("content", [{"label": "MAYBE", "rationale": "x"}, {"label": "SAME_PRINCIPLE", "rationale": ""}, {}])
def test_unusable_judge_output_is_an_error(content: dict) -> None:
    with pytest.raises(InvalidModelOutputError):
        PrincipleJudge(StubProvider(content)).judge(CHAMPION_CORRECTION, CHAMPION, _inferred())


def test_judge_never_goes_live_without_a_cassette(tmp_path) -> None:  # noqa: ANN001
    with pytest.raises(CassetteNotFoundError):
        PrincipleJudge(FakeProvider(tmp_path, "fam")).judge(CHAMPION_CORRECTION, CHAMPION, _inferred())


def test_judge_returns_label_and_model() -> None:
    j = PrincipleJudge(StubProvider({"label": "PARTIAL_PRINCIPLE", "rationale": "r"})).judge(
        Correction(account_state_summary="s", agent_proposal="p", human_edit="e"), CHAMPION, _inferred())
    assert (j.label, j.model) == ("PARTIAL_PRINCIPLE", "stub")


def test_contract_example_k17_v1_has_unreachable_exceptions() -> None:
    """K17 v1 (contracts/examples) scopes to champion_status = active, so its delegation/departure exceptions never
    fire; the fixtures' K17 v2 widened the signature for exactly this reason (ADR-0011)."""
    from test_contracts import example

    k17 = InferredKnowledge.model_validate(example("knowledge"))
    result = exception_quality(k17.exceptions, k17.exceptions, k17.scope)
    assert len(result.unreachable) == 2 and result.label == "EXCEPTIONS_MISSED"


def test_engaged_statuses_match_the_buying_group_vocabulary() -> None:
    """Python entailment and the Go matcher (disengagedStatuses) share one rule: departed, inactive and disengaged
    members do not hold a role; every other status does (ADR-0013)."""
    statuses = set(load_json(SCHEMAS / "account_state.v1.json")["properties"]["buying_group"]["items"]["properties"]["status"]["enum"])
    assert ENGAGED == statuses - {"departed", "inactive", "disengaged"}
