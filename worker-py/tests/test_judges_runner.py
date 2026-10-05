"""WP18 (HAR-116): judges run concurrently (HAR-97 §7) and repeatedly (§20), each trial with its own provider."""
from __future__ import annotations

import threading

import pytest

from judge_doubles import CATALOG, MODEL, RUBRICS, RUN_ID, ScriptedJudgeProvider, answer, context

from ghost_worker.errors import ConfigError
from ghost_worker.judges import JudgeRequest, JudgeRun, judge_many, run_suite
from ghost_worker.llm.provider import LLMResult

ALL_PASS = {r.schema_name: answer(r) for r in RUBRICS.values()}


class BarrierProvider(ScriptedJudgeProvider):
    """Blocks each call until `parties` calls are in flight at once: only a concurrent runner gets through."""

    def __init__(self, parties: int) -> None:
        super().__init__(ALL_PASS)
        self.barrier = threading.Barrier(parties, timeout=5)

    def complete_json(self, **kwargs) -> LLMResult:
        self.barrier.wait()
        return super().complete_json(**kwargs)


def test_routed_judges_run_concurrently() -> None:
    ctx = context()
    provider = BarrierProvider(parties=3)
    suite = run_suite(ctx, RUN_ID, lambda t: provider, RUBRICS, CATALOG, eval_types=["grounding", "momentum",
                                                                                    "buyer_readiness"], max_workers=3)
    assert len(suite.outcomes) == 3 and all(o.ok for o in suite.outcomes)
    assert len({c["thread"] for c in provider.calls}) == 3


def test_run_suite_routes_by_default_and_reports_blocking() -> None:
    answers = dict(ALL_PASS)
    answers[RUBRICS["buyer_readiness"].schema_name] = answer(RUBRICS["buyer_readiness"], verdict="fail",
                                                             label="TOO_EARLY", blocks=True)
    suite = run_suite(context(), RUN_ID, lambda t: ScriptedJudgeProvider(answers), RUBRICS, CATALOG)
    assert tuple(o.eval_type for o in suite.outcomes) == suite.route.selected
    assert suite.blocked() and len(suite.results()) == len(suite.route.selected)


def test_trials_use_their_own_provider_and_distinct_result_ids() -> None:
    providers = {t: ScriptedJudgeProvider(ALL_PASS, model=f"{MODEL}#{t}") for t in (1, 2, 3)}
    suite = run_suite(context(), RUN_ID, providers.__getitem__, RUBRICS, CATALOG, eval_types=["grounding"], trials=3)
    assert [o.trial for o in suite.outcomes] == [1, 2, 3]
    assert [o.result["model"] for o in suite.outcomes] == [f"{MODEL}#{t}" for t in (1, 2, 3)]
    assert len({o.result["id"] for o in suite.outcomes}) == 3
    assert all(len(p.calls) == 1 for p in providers.values())


def test_judge_many_keeps_request_order_across_cases() -> None:
    requests = [JudgeRequest(key=f"case-{i}", context=context(), eval_type=name,
                             run=JudgeRun(agent_run_id=RUN_ID, trial=1))
                for i, name in enumerate(["momentum", "grounding", "business_case"])]
    batch = judge_many(requests, lambda t: ScriptedJudgeProvider(ALL_PASS), RUBRICS, CATALOG, max_workers=2)
    assert [(r.key, o.eval_type) for r, o in batch.pairs] == [(r.key, r.eval_type) for r in requests]


@pytest.mark.parametrize(("kwargs", "fragment"), [({"trials": 0}, "trials"), ({"max_workers": 0}, "max_workers"),
                                                   ({"eval_types": ["recipient_correctness"]}, "no rubric")])
def test_invalid_runner_settings_are_explicit_errors(kwargs: dict, fragment: str) -> None:
    with pytest.raises(ConfigError, match=fragment):
        run_suite(context(), RUN_ID, lambda t: ScriptedJudgeProvider(ALL_PASS), RUBRICS, CATALOG, **kwargs)
