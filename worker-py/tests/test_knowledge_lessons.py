"""HAR-97 L4 knowledge evals on the spec-derived seed gold, replaying hand-written judge cassettes (no network)."""
from __future__ import annotations

import json
from collections import Counter
from pathlib import Path

import pytest

import knowledge_lessons as kl
from ghost_worker.knowledge_evals import PrincipleJudge, evaluate
from ghost_worker.knowledge_evals.principle import SCHEMA, SCHEMA_NAME, SYSTEM, build_user_prompt
from ghost_worker.llm.fake_provider import FakeProvider, cassette_key, model_family
from ghost_worker.settings import Settings

CASSETTES = Path(__file__).resolve().parents[1] / "cassettes" / "knowledge"
FAMILY = model_family(Settings(_env_file=None).ghost_model)


class CountingProvider:
    """Replays cassettes and counts judge calls (the judge must not run when there is nothing to compare)."""

    def __init__(self) -> None:
        self.inner, self.calls = FakeProvider(CASSETTES, FAMILY), 0

    def complete_json(self, **kwargs: object):  # noqa: ANN201 - mirrors LLMProvider
        self.calls += 1
        return self.inner.complete_json(**kwargs)


def run(case: kl.LessonCase):  # noqa: ANN201
    provider = CountingProvider()
    report = evaluate(case.correction, case.reference, case.inferred, PrincipleJudge(provider))
    return report, provider.calls


@pytest.mark.parametrize("case", kl.CASES, ids=[c.id for c in kl.CASES])
def test_case_labels(case: kl.LessonCase) -> None:
    report, calls = run(case)
    fid, sq = report.extraction_fidelity, report.scope_exception_quality
    assert fid.reusable == case.reusable
    assert (sq.scope.label if sq.scope else None) == case.scope
    assert (sq.exceptions.label if sq.exceptions else None) == case.exceptions
    assert (fid.principle.label if fid.principle else None) == case.judge_label
    assert (fid.verdict, sq.verdict) == (case.fidelity, case.scope_quality)
    assert calls == (1 if case.judge_label else 0)


def test_always_cc_the_champion_is_flagged_as_over_generalised() -> None:
    """HAR-97 L4's named bad inference."""
    report, _ = run(next(c for c in kl.CASES if c.id == "always_cc_the_champion"))
    assert report.extraction_fidelity.over_generalised
    assert set(report.scope_exception_quality.scope.missing) == {"motion eq 'expansion'", "diff.new_stakeholder_entered exists"}
    assert report.scope_exception_quality.exceptions.recall == 0.0


def test_dropping_the_transition_scope_is_named() -> None:
    report, _ = run(next(c for c in kl.CASES if c.id == "reorg_lesson_without_transition_scope"))
    assert report.scope_exception_quality.scope.transition_scope_missing


def test_unreachable_exceptions_are_reported() -> None:
    report, _ = run(next(c for c in kl.CASES if c.id == "champion_exceptions_unreachable"))
    assert len(report.scope_exception_quality.exceptions.unreachable) == 3


def test_seed_gold_covers_every_label() -> None:
    labels = Counter()
    for case in kl.CASES:
        labels.update({case.reusable, case.scope, case.exceptions, case.judge_label, case.fidelity, case.scope_quality})
    expected = {"PRINCIPLE_DETECTED", "PRINCIPLE_MISSED", "SPURIOUS_PRINCIPLE", "CORRECT_ABSTENTION", "SCOPE_MATCH",
                "OVER_GENERALISED", "OVER_NARROW", "WRONG_SCOPE", "EXCEPTIONS_CAPTURED", "EXCEPTIONS_PARTIAL",
                "EXCEPTIONS_MISSED", "NO_EXCEPTIONS_EXPECTED", "SAME_PRINCIPLE", "PARTIAL_PRINCIPLE",
                "DIFFERENT_PRINCIPLE", "pass", "fail"}
    assert expected <= set(labels)


def test_cassettes_are_hand_written_and_consistent() -> None:
    """One cassette key never carries two gold labels, and every cassette is used."""
    by_key: dict[str, str] = {}
    for case in kl.CASES:
        if case.judge_label and case.inferred:
            key = cassette_key(FAMILY, SYSTEM, build_user_prompt(case.correction, case.reference, case.inferred),
                               SCHEMA_NAME, SCHEMA)
            assert by_key.setdefault(key, case.judge_label) == case.judge_label
    files = {p.stem for p in CASSETTES.glob("*.json")}
    assert files == set(by_key)
    assert all(json.loads((CASSETTES / f"{k}.json").read_text(encoding="utf-8"))["hand_written"] for k in files)
