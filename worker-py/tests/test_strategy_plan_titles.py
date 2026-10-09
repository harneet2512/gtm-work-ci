"""HAR-124: qwen3.8-flash sometimes omits `title` on a planned strategy. That cosmetic slip is repaired deterministically; substance never is."""
from __future__ import annotations

import copy
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent))

import strategy_doubles as sd  # noqa: E402
from ghost_worker.draft.schemas import parse_model_output  # noqa: E402
from ghost_worker.errors import InvalidModelOutputError  # noqa: E402
from ghost_worker.strategies.schemas import StrategyPlan, derive_title, normalize_plan  # noqa: E402


def plan_content() -> dict:
    """The default plan as plain dicts (the provider's own content is a frozen mapping: see the frozen test below)."""
    return {"strategies": [sd.planned("send_package_and_wait"), sd.planned("add_security_lead", to=((sd.SECURITY, "lead"),), cc=((sd.CHAMPION, "keep informed"),)),
                           sd.planned("clarify_scope_first", cc=())]}


def test_missing_blank_and_non_string_titles_are_derived_from_type_and_description() -> None:
    content = plan_content()
    del content["strategies"][1]["title"]
    content["strategies"][2]["title"] = "   "
    content["strategies"][0]["title"] = None
    plan = parse_model_output(StrategyPlan, normalize_plan(content), "strategy plan")
    for strategy in plan.strategies:
        assert 1 <= len(strategy.title) <= 80
        assert strategy.title.lower().startswith(strategy.strategy_type.replace("_", " ")[:8])
    assert plan.strategies[1].title == "Add security lead: add_security_lead"


def test_the_providers_frozen_content_is_repaired_too() -> None:
    """TurnResult.content is a FrozenMap of tuples: the repair must not be skipped because it is not a dict."""
    turn = sd.default_plan()
    content = {"strategies": [dict(s) for s in turn.content["strategies"]]}
    del content["strategies"][1]["title"]
    frozen = type(turn)(content=content, model=turn.model, usage={}).content
    assert not isinstance(frozen, dict)
    plan = parse_model_output(StrategyPlan, normalize_plan(frozen), "strategy plan")
    assert plan.strategies[1].title.startswith("Add security lead")


def test_the_derived_title_is_deterministic_and_cut_at_a_word_to_the_limit() -> None:
    long_desc = "keep going " * 30
    title = derive_title("reply_long_term_cost_clarification", long_desc)
    assert len(title) <= 80 and title.endswith("...") and title == derive_title("reply_long_term_cost_clarification", long_desc)
    assert derive_title("wait", "") == "Wait"
    assert derive_title("convert_review", "Offer a working session, then follow up") == "Convert review: Offer a working session"


def test_an_overlong_title_is_cut_and_a_good_title_is_left_alone() -> None:
    content = plan_content()
    content["strategies"][0]["title"] = "x " * 60
    content["strategies"][1]["title"] = "A fine title"
    out = normalize_plan(content)
    assert len(out["strategies"][0]["title"]) <= 80 and out["strategies"][1]["title"] == "A fine title"


def test_normalizing_does_not_modify_its_input() -> None:
    content = plan_content()
    del content["strategies"][0]["title"]
    before = copy.deepcopy(content)
    normalize_plan(content)
    assert content == before


@pytest.mark.parametrize("field", ["rationale", "evidence_refs", "to", "five_questions", "action"])
def test_missing_substance_still_fails_validation(field: str) -> None:
    content = plan_content()
    del content["strategies"][0][field]
    with pytest.raises(InvalidModelOutputError):
        parse_model_output(StrategyPlan, normalize_plan(content), "strategy plan")


def test_non_plan_content_passes_through_to_validation_unchanged() -> None:
    for junk in ({}, {"strategies": "no"}, [], None, {"strategies": [1, 2, 3]}):
        assert normalize_plan(junk) == junk
        with pytest.raises(InvalidModelOutputError):
            parse_model_output(StrategyPlan, normalize_plan(junk), "strategy plan")
