"""Run judges concurrently (HAR-97 §7: independent semantic judges run in parallel) and repeatedly (§20: LLM
judges are nondeterministic, so each case can be judged N times)."""
from __future__ import annotations

from collections.abc import Callable, Mapping, Sequence
from concurrent.futures import ThreadPoolExecutor

from pydantic import BaseModel, ConfigDict, Field

from ..errors import ConfigError
from ..llm.provider import LLMProvider
from .catalog import EvalCatalog
from .context import JudgeContext
from .engine import Clock, JudgeOutcome, JudgeRun, judge, utc_now
from .rubric import Rubric
from .routing import RouteDecision, route

ProviderForTrial = Callable[[int], LLMProvider]
DEFAULT_MAX_WORKERS = 8


class JudgeRequest(BaseModel):
    model_config = ConfigDict(frozen=True)

    key: str  # caller's handle, e.g. the benchmark case id
    context: JudgeContext
    eval_type: str
    run: JudgeRun


class SuiteResult(BaseModel):
    model_config = ConfigDict(frozen=True)

    route: RouteDecision
    outcomes: tuple[JudgeOutcome, ...]

    def results(self, trial: int = 1) -> tuple[dict, ...]:
        return tuple(o.result for o in self.outcomes if o.ok and o.trial == trial)

    def blocked(self, trial: int = 1) -> bool:
        return any(r["blocking"] for r in self.results(trial))


class Batch(BaseModel):
    """Requests in the order given, each with its outcome."""

    model_config = ConfigDict(frozen=True)

    pairs: tuple[tuple[JudgeRequest, JudgeOutcome], ...] = Field(default=())


def _check(requests: Sequence[JudgeRequest], rubrics: Mapping[str, Rubric]) -> None:
    missing = sorted({r.eval_type for r in requests} - set(rubrics))
    if missing:
        raise ConfigError(f"no rubric for {missing}")


def judge_many(requests: Sequence[JudgeRequest], provider_for_trial: ProviderForTrial,
               rubrics: Mapping[str, Rubric], catalog: EvalCatalog, *, max_workers: int = DEFAULT_MAX_WORKERS,
               clock: Clock = utc_now) -> Batch:
    """Every request on a thread pool; outcomes keep request order. Each trial gets its own provider (e.g. its
    own cassette directory)."""
    if max_workers < 1:
        raise ConfigError("max_workers must be >= 1")
    _check(requests, rubrics)
    providers = {t: provider_for_trial(t) for t in sorted({r.run.trial for r in requests})}

    def one(request: JudgeRequest) -> JudgeOutcome:
        return judge(providers[request.run.trial], rubrics[request.eval_type], catalog, request.context,
                     request.run, clock=clock)

    with ThreadPoolExecutor(max_workers=max_workers, thread_name_prefix="judge") as pool:
        outcomes = list(pool.map(one, requests))
    return Batch(pairs=tuple(zip(requests, outcomes, strict=True)))


def run_suite(context: JudgeContext, agent_run_id: str, provider_for_trial: ProviderForTrial,
              rubrics: Mapping[str, Rubric], catalog: EvalCatalog, *, draft_index: int = 1, trials: int = 1,
              eval_types: Sequence[str] | None = None, max_workers: int = DEFAULT_MAX_WORKERS,
              clock: Clock = utc_now) -> SuiteResult:
    """Route (unless `eval_types` is given) and judge one candidate, `trials` times."""
    if trials < 1:
        raise ConfigError("trials must be >= 1")
    decision = route(context, rubrics, catalog)
    selected = decision.selected if eval_types is None else catalog.order(set(eval_types))
    requests = [JudgeRequest(key=agent_run_id, context=context, eval_type=name,
                             run=JudgeRun(agent_run_id=agent_run_id, draft_index=draft_index, trial=t))
                for t in range(1, trials + 1) for name in selected]
    batch = judge_many(requests, provider_for_trial, rubrics, catalog, max_workers=max_workers, clock=clock)
    return SuiteResult(route=decision, outcomes=tuple(o for _, o in batch.pairs))
