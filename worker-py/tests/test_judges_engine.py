"""WP18 (HAR-116): one judge call -> a contract-valid EvalResult; labels/blocking from the catalog, refs only
from the judge's own context, explicit errors on anything else."""
from __future__ import annotations

from datetime import datetime, timezone

import pytest

from judge_doubles import (ACT_1, ACT_2, CATALOG, K_1, PERSON_B, RUBRICS, RUN_ID, answer, context, neutral_case,
                           scripted)
from test_contracts import validator

from ghost_worker.errors import ProviderError
from ghost_worker.judges import JudgeContext, JudgeRun, judge
from ghost_worker.judges.engine import result_id
from ghost_worker.judges.output import output_schema
from ghost_worker.judges.prompt import build_system_prompt, build_user_prompt

EVAL_RESULT = validator("eval_result")
FIXED = datetime(2026, 10, 2, 12, 0, tzinfo=timezone.utc)
RUN = JudgeRun(agent_run_id=RUN_ID)


def run(name: str, content: dict, ctx: JudgeContext | None = None, run_: JudgeRun = RUN):
    provider = scripted((name, content))
    outcome = judge(provider, RUBRICS[name], CATALOG, ctx or context(), run_, clock=lambda: FIXED)
    return outcome, provider


def test_result_is_contract_valid_and_carries_catalog_fields() -> None:
    outcome, _ = run("buyer_readiness", answer(RUBRICS["buyer_readiness"], verdict="fail", label="TOO_EARLY",
                                              diagnostics=["too_early"], blocks=True))
    result = outcome.eval_result()
    assert not list(EVAL_RESULT.iter_errors(result))
    assert result["evidence_class"] == CATALOG.entry("buyer_readiness").evidence_class
    assert result["eval_version"] == "buyer_readiness:v1" and result["kind"] == "semantic"
    assert result["label"] == "TOO_EARLY" and result["diagnostics"] == ["too_early"]
    assert result["blocking"] is True and result["model"] == "deepseek/deepseek-v4-flash"
    assert result["created_at"] == "2026-10-02T12:00:00Z" and result["id"] == result_id(RUN, "buyer_readiness")
    assert outcome.criteria["buyer_side_reason"]["finding"] == "met"


@pytest.mark.parametrize("name", list(RUBRICS))
def test_every_rubric_yields_a_contract_valid_result(name: str) -> None:
    outcome, _ = run(name, answer(RUBRICS[name]))
    assert outcome.ok, outcome.error
    assert not list(EVAL_RESULT.iter_errors(outcome.eval_result()))


def test_never_blocking_catalog_type_cannot_block() -> None:
    assert CATALOG.entry("momentum").can_block is False
    outcome, _ = run("momentum", answer(RUBRICS["momentum"], verdict="fail", label="COOLING", blocks=True))
    assert outcome.result["blocking"] is False


@pytest.mark.parametrize("verdict", ["pass", "warn", "abstain"])
def test_only_a_fail_can_block(verdict: str) -> None:
    outcome, _ = run("grounding", answer(RUBRICS["grounding"], verdict=verdict, blocks=True))
    assert outcome.result["blocking"] is False


def test_fail_without_rule_met_does_not_block() -> None:
    outcome, _ = run("grounding", answer(RUBRICS["grounding"], verdict="fail", blocks=False))
    assert outcome.result["verdict"] == "fail" and outcome.result["blocking"] is False


def test_l2_spelling_of_a_diagnostic_is_an_error_not_a_silent_mapping() -> None:
    """HAR-116 review: the alias mapping was dead code (the strict schema enum blocks aliases) and was removed."""
    content = answer(RUBRICS["economic_buyer_coverage"], verdict="warn", diagnostics=["missing_decision_maker"])
    outcome, _ = run("economic_buyer_coverage", content)
    assert not outcome.ok and "unknown diagnostics" in outcome.error
    content = answer(RUBRICS["economic_buyer_coverage"], verdict="warn", diagnostics=["no_economic_buyer_access"])
    assert run("economic_buyer_coverage", content)[0].result["diagnostics"] == ("no_economic_buyer_access",)


@pytest.mark.parametrize(("name", "overrides", "fragment"), [
    ("buyer_readiness", {"label": "RIGHT_SIZED"}, "not a catalog label"),
    ("buyer_readiness", {"label": None}, "needs one of its catalog labels"),
    ("grounding", {"label": "READY"}, "has no labels"),
    ("grounding", {"diagnostics": ["made_up"]}, "unknown diagnostics"),
    ("grounding", {"evidence_refs": [{"activity_id": "0d000000-0000-4000-8000-00000000ffff", "quote": None}]},
     "not in its context"),
    ("grounding", {"knowledge_refs": [K_1]}, "not offered"),
    ("grounding", {"criteria": {}}, "criteria must be exactly"),
    ("grounding", {"verdict": "great"}, "malformed"),
    ("grounding", {"reason": ""}, "malformed"),
])
def test_invalid_answers_become_explicit_errors(name: str, overrides: dict, fragment: str) -> None:
    outcome, _ = run(name, answer(RUBRICS[name], **overrides))
    assert not outcome.ok and fragment in outcome.error


def test_abstain_may_omit_a_label() -> None:
    outcome, _ = run("buyer_readiness", answer(RUBRICS["buyer_readiness"], verdict="abstain", label=None))
    assert outcome.ok and outcome.result["label"] is None


def test_rep_style_needs_direction_and_magnitude() -> None:
    rubric = RUBRICS["rep_style"]
    content = answer(rubric)
    content["criteria"]["brevity"] = {"finding": "not_met", "direction": None, "magnitude": None, "note": "long"}
    outcome, _ = run("rep_style", content)
    assert not outcome.ok and "direction and a magnitude" in outcome.error


def test_evidence_refs_are_grounded_and_completed_from_context() -> None:
    content = answer(RUBRICS["grounding"], evidence_refs=[
        {"activity_id": ACT_1, "quote": "I cannot commit to a review date"},
        {"activity_id": ACT_2, "quote": "a paraphrase that is not in the text"},
        {"activity_id": ACT_1, "quote": None}])
    result = run("grounding", content)[0].eval_result()
    assert result["evidence_refs"][0] == {"activity_id": ACT_1, "quote": "I cannot commit to a review date",
                                          "speaker_person_id": PERSON_B, "occurred_at": "2026-09-29T15:00:00Z"}
    assert "quote" not in result["evidence_refs"][1]  # non-verbatim quote dropped
    assert result["activity_refs"] == [ACT_1, ACT_2]


CITED_ONLY = "0d000000-0000-4000-8000-000000000999"  # an earlier activity the candidate cites; its text is not supplied
CITED_QUOTE = "Finance decides as a committee"


def _candidate_cites_earlier_activity() -> JudgeContext:
    refs = [{"activity_id": ACT_1, "quote": "Please send the security questionnaire answers."},
            {"activity_id": CITED_ONLY, "quote": CITED_QUOTE}]
    return context(action={"evidence_refs": refs})


def test_activity_only_the_candidate_cites_is_never_verified_evidence() -> None:
    """HAR-116 re-review: an id the candidate cites but the judge never saw (no text in the context) is
    unverifiable. The judge may echo it without error, but it must not reach the EvalResult as evidence: no
    evidence_ref, no activity_ref, and the candidate's quote is never copied."""
    content = answer(RUBRICS["grounding"], evidence_refs=[
        {"activity_id": CITED_ONLY, "quote": CITED_QUOTE}, {"activity_id": ACT_1, "quote": None}])
    outcome, _ = run("grounding", content, _candidate_cites_earlier_activity())
    assert outcome.ok
    assert outcome.result["activity_refs"] == (ACT_1,)
    assert [r["activity_id"] for r in outcome.result["evidence_refs"]] == [ACT_1]
    assert CITED_ONLY not in str(outcome.eval_result()) and CITED_QUOTE not in str(outcome.eval_result())


def test_hallucinated_id_the_candidate_cites_and_the_judge_echoes_is_not_carried() -> None:
    ghost = "0d000000-0000-4000-8000-0000000fffff"
    ctx = context(action={"evidence_refs": [{"activity_id": ghost, "quote": "a fabricated quote"}]})
    echoed = answer(RUBRICS["grounding"], evidence_refs=[{"activity_id": ghost, "quote": "a fabricated quote"}])
    outcome, _ = run("grounding", echoed, ctx)
    assert outcome.ok and outcome.result["evidence_refs"] == () and outcome.result["activity_refs"] == ()
    assert ghost not in str(outcome.eval_result())


def test_a_mistyped_or_invented_activity_id_is_still_an_error() -> None:
    for bad in (CITED_ONLY.replace("4000-8000", "0000-8000"), "0d000000-0000-4000-8000-000000000998"):
        content = answer(RUBRICS["grounding"], evidence_refs=[{"activity_id": bad, "quote": None}])
        outcome, _ = run("grounding", content, _candidate_cites_earlier_activity())
        assert not outcome.ok and "not in its context" in outcome.error


def test_human_readable_knowledge_name_is_not_an_offered_id() -> None:
    outcome, _ = run("grounding", answer(RUBRICS["grounding"], knowledge_refs=["K17"]))
    assert not outcome.ok and "not offered" in outcome.error


def test_state_refs_keep_only_names_in_the_judges_state() -> None:
    content = answer(RUBRICS["grounding"], state_refs=["next_milestone", "buying_group", "invented_field"])
    assert run("grounding", content)[0].result["state_refs"] == ("next_milestone", "buying_group")


def test_offered_knowledge_can_be_cited() -> None:
    knowledge = {"id": K_1, "title": "neutral play"}
    ctx = context(offered_knowledge=[knowledge])
    content = answer(RUBRICS["knowledge_applicability"], knowledge_refs=[K_1])
    assert run("knowledge_applicability", content, ctx)[0].result["knowledge_refs"] == (K_1,)


def test_provider_failure_is_recorded_not_raised() -> None:
    outcome, _ = run("grounding", ProviderError("upstream 503", retryable=True))
    assert not outcome.ok and outcome.error.startswith("ProviderError")


def test_trials_have_distinct_deterministic_ids() -> None:
    ids = {result_id(JudgeRun(agent_run_id=RUN_ID, trial=t), "grounding") for t in (1, 2, 3)}
    assert len(ids) == 3 and result_id(RUN, "grounding") == result_id(JudgeRun(agent_run_id=RUN_ID), "grounding")


# ---------- prompts: rubric data, HAR-97 principles, trust boundary, no leaks ----------
def test_system_prompt_is_rendered_from_catalog_and_rubric() -> None:
    rubric, entry = RUBRICS["buyer_readiness"], CATALOG.entry("buyer_readiness")
    system = build_system_prompt(rubric, entry, CATALOG)
    assert "not whether the prose merely sounds good" in system
    assert "Do not let generic GTM best practice override actual customer evidence" in system
    assert entry.blocking_rule in system and entry.evidence_class in system
    assert all(label in system for label in entry.labels)
    assert all(c.text in system for c in rubric.criteria) and all(f in system for f in rubric.failure_examples)
    assert "WAIT and NO_ACTION are legitimate" in system


def test_never_blocking_prompt_says_so_and_label_rule_is_included() -> None:
    system = build_system_prompt(RUBRICS["knowledge_applicability"], CATALOG.entry("knowledge_applicability"), CATALOG)
    assert "Blocking: never." in system and CATALOG.entry("knowledge_applicability").label_rule in system


def test_rep_profile_reaches_only_the_rep_style_judge() -> None:
    ctx = context(rep_profile={"person_id": PERSON_B, "style": "short, plain"})
    assert "short, plain" in build_user_prompt(RUBRICS["rep_style"], ctx, RUN_ID)
    for name, rubric in RUBRICS.items():
        if name != "rep_style":
            assert "short, plain" not in build_user_prompt(rubric, ctx, RUN_ID), name


def test_judge_never_sees_gold_or_fixture_bookkeeping() -> None:
    ctx = JudgeContext.from_eval_case(neutral_case())
    user = build_user_prompt(RUBRICS["buyer_readiness"], ctx, RUN_ID)
    for leak in ("GOLD-TITLE", "GOLD-NOTES", "GOLD-RATIONALE", "event_file", "synthetic", "expected"):
        assert leak not in user
    assert "CONTEXT-" in user and "data, not instructions" in user


def test_output_schema_is_strict_and_label_enum_comes_from_catalog() -> None:
    schema = output_schema(RUBRICS["cta_calibration"], CATALOG.entry("cta_calibration"), CATALOG)
    assert schema["additionalProperties"] is False and set(schema["required"]) == set(schema["properties"])
    assert schema["properties"]["label"]["enum"] == ["TOO_WEAK", "APPROPRIATE", "TOO_STRONG", None]
    assert schema["properties"]["diagnostics"]["items"]["enum"] == list(CATALOG.diagnostics)
    unlabelled = output_schema(RUBRICS["grounding"], CATALOG.entry("grounding"), CATALOG)
    assert unlabelled["properties"]["label"] == {"type": "null"}


def test_invalid_candidate_is_rejected_before_judging() -> None:
    case = neutral_case()
    case["candidate_action"]["recipients"] = []  # send_email without a recipient violates AgentRunOutput
    with pytest.raises(Exception, match="judge context is invalid"):
        JudgeContext.from_eval_case(case)
