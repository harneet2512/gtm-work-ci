"""HAR-145 per-request usage metering: the meter sums what completions report, the metered provider records calls,
tool calls, retries and time, and the five worker responses carry the summary as `usage` (a metric, never an eval)."""
from __future__ import annotations

import threading
from types import SimpleNamespace

import pytest

import draft_doubles as dbl
import draft_situations as sit
import strategy_doubles as sd
from ghost_worker.errors import ProviderError
from ghost_worker.llm.litellm_provider import _usage_dict
from ghost_worker.llm.provider import LLMResult, ToolCall, TurnResult
from ghost_worker.llm.usage import MeteredProvider, UsageMeter, note_retry, take_retries
from ghost_worker.models.usage import UsageSummary
from openapi_schemas import draft_response_validator, response_validator
from test_contracts import example, validator
from test_draft_api import make_client
from test_human_delta import client_for as delta_client
from test_human_delta import labeled as delta_labeled
from test_human_delta import request_body as delta_request
from test_judge_api import post as judge_post
from test_strategies_api import client, revise_request, revision, scripted

SCHEMA = {"type": "object", "properties": {"a": {"type": "integer"}}}


class Clock:
    def __init__(self) -> None:
        self.now = 100.0

    def __call__(self) -> float:
        return self.now


def usage(**kw) -> dict:
    return {"prompt_tokens": 100, "completion_tokens": 20, **kw}


# --- the meter -----------------------------------------------------------------------------------------------------


def test_the_meter_sums_tokens_calls_tools_retries_time_and_cost() -> None:
    meter = UsageMeter()
    meter.record_call(usage(cached_tokens=40, reasoning_tokens=5, cost=0.002), "m1", elapsed_s=1.5, retries=1, tool_calls=2)
    meter.record_call(usage(cost=0.003, cached_tokens=0, reasoning_tokens=0), "m2", elapsed_s=0.25, retries=0, tool_calls=0)
    s = meter.summary()
    assert (s.model_calls, s.input_tokens, s.output_tokens) == (2, 200, 40)
    assert (s.cached_input_tokens, s.reasoning_tokens, s.tool_calls, s.retries, s.model_ms) == (40, 5, 2, 1, 1750)
    assert s.cost_usd == pytest.approx(0.005) and s.models == ("m1", "m2")


def test_an_empty_meter_reports_zeros_and_no_cost() -> None:
    s = UsageMeter().summary()
    assert s.model_calls == 0 and s.input_tokens == 0 and s.cost_usd is None and s.models == ()
    assert s.cached_input_tokens is None and s.reasoning_tokens is None and s.usage_source == "live"


def test_cached_and_reasoning_are_null_unless_every_call_reported_them() -> None:
    meter = UsageMeter()
    meter.record_call(usage(cached_tokens=0, reasoning_tokens=0), "m", elapsed_s=0, retries=0, tool_calls=0)
    s = meter.summary()
    assert s.cached_input_tokens == 0 and s.reasoning_tokens == 0  # a reported zero is a measured zero
    meter.record_call(usage(), "m", elapsed_s=0, retries=0, tool_calls=0)  # this one reported neither
    s = meter.summary()
    assert s.cached_input_tokens is None and s.reasoning_tokens is None
    assert s.input_tokens == 200  # what was reported still counts


# --- replay never reports spend ------------------------------------------------------------------------------------


def test_a_replayed_request_reports_no_spend_and_says_it_was_replayed(tmp_path) -> None:
    from ghost_worker.llm.fake_provider import FakeProvider, cassette_key, write_cassette

    key = cassette_key("fam", "s", "u", "n", SCHEMA)
    recorded = {"content": {"a": 1}, "model": "recorded-model", "usage": usage(cached_tokens=10, cost=0.5)}
    write_cassette(tmp_path, key, {"key": key, "response": recorded})
    meter = UsageMeter()
    result = MeteredProvider(FakeProvider(tmp_path, "fam"), meter).complete_json(system="s", user="u", schema=SCHEMA,
                                                                                 schema_name="n")
    assert result.content == {"a": 1}
    s = meter.summary()
    assert s.usage_source == "replay"
    assert (s.model_calls, s.input_tokens, s.output_tokens, s.tool_calls, s.model_ms, s.retries) == (0, 0, 0, 0, 0, 0)
    assert s.cost_usd is None and s.cached_input_tokens is None and s.reasoning_tokens is None and s.models == ()


def test_replay_is_still_replay_behind_the_breaker_and_the_concurrency_limit(tmp_path) -> None:
    from ghost_worker.llm.breaker import BreakerProvider, CircuitBreaker
    from ghost_worker.llm.fake_provider import FakeProvider
    from ghost_worker.llm.limited import ConcurrencyLimitedProvider

    wrapped = ConcurrencyLimitedProvider(BreakerProvider(FakeProvider(tmp_path, "f"), CircuitBreaker()),
                                         max_concurrent=1, acquire_timeout_s=1)
    meter = UsageMeter()
    MeteredProvider(wrapped, meter)
    assert meter.summary().usage_source == "replay"


def test_a_live_provider_reports_live() -> None:
    meter = UsageMeter()
    MeteredProvider(Inner(Clock()), meter).complete_json(system="s", user="u", schema=SCHEMA, schema_name="n")
    assert meter.summary().usage_source == "live"


def test_cost_is_null_unless_every_call_reported_one() -> None:
    meter = UsageMeter()
    meter.record_call(usage(cost=0.002), "m", elapsed_s=0, retries=0, tool_calls=0)
    meter.record_call(usage(), "m", elapsed_s=0, retries=0, tool_calls=0)  # this provider call reported no cost
    assert meter.summary().cost_usd is None


def test_a_provider_that_names_the_figures_differently_is_still_counted() -> None:
    meter = UsageMeter()
    meter.record_call({"input_tokens": 70, "output_tokens": 9, "cache_read_input_tokens": 30}, "m", elapsed_s=0, retries=0, tool_calls=0)
    s = meter.summary()
    assert (s.input_tokens, s.output_tokens, s.cached_input_tokens) == (70, 9, 30)
    assert s.reasoning_tokens is None  # not reported, so not claimed as 0


def test_cached_and_reasoning_are_parts_of_their_totals_never_more() -> None:
    meter = UsageMeter()
    meter.record_call({"prompt_tokens": 10, "completion_tokens": 4, "cached_tokens": 99, "reasoning_tokens": 99}, "m",
                      elapsed_s=0, retries=0, tool_calls=0)
    s = meter.summary()
    assert s.cached_input_tokens == 10 and s.reasoning_tokens == 4


@pytest.mark.parametrize("bad", [-5, "12", None, True, float("nan")])
def test_garbage_in_a_usage_dict_counts_as_not_reported(bad: object) -> None:
    meter = UsageMeter()
    meter.record_call({"prompt_tokens": bad, "completion_tokens": bad, "cost": bad}, "m", elapsed_s=0, retries=0, tool_calls=0)
    s = meter.summary()
    assert s.input_tokens == 0 and s.output_tokens == 0 and s.cost_usd is None and s.model_calls == 1


def test_a_failed_request_still_counts_its_retries_and_time_but_no_call() -> None:
    meter = UsageMeter()
    meter.record_failure(elapsed_s=2.0, retries=3)
    s = meter.summary()
    assert (s.model_calls, s.retries, s.model_ms, s.input_tokens) == (0, 3, 2000, 0)


def test_concurrent_recording_loses_nothing() -> None:
    meter = UsageMeter()

    def work() -> None:
        for _ in range(200):
            meter.record_call(usage(cost=0.001), "m", elapsed_s=0.001, retries=1, tool_calls=1)

    threads = [threading.Thread(target=work) for _ in range(8)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    s = meter.summary()
    assert (s.model_calls, s.input_tokens, s.output_tokens, s.retries, s.tool_calls) == (1600, 160000, 32000, 1600, 1600)
    assert s.models == ("m",)


# --- the metered provider ------------------------------------------------------------------------------------------


class Inner:
    """A provider double: answers, optionally spends time on the fake clock and notes retries like litellm does."""

    def __init__(self, clock: Clock, *, seconds: float = 0.0, retries: int = 0, error: Exception | None = None) -> None:
        self.clock, self.seconds, self.retries, self.error = clock, seconds, retries, error

    def _spend(self) -> None:
        self.clock.now += self.seconds
        for _ in range(self.retries):
            note_retry()
        if self.error:
            raise self.error

    def complete_json(self, *, system: str, user: str, schema: dict, schema_name: str) -> LLMResult:
        self._spend()
        return LLMResult(content={"a": 1}, model="m-json", usage=usage(cached_tokens=10, cost=0.001))

    def complete_turn(self, *, system: str, messages: list, tools: list, schema: dict, schema_name: str) -> TurnResult:
        self._spend()
        calls = (ToolCall(id="1", name="state", arguments={}), ToolCall(id="2", name="people", arguments={}))
        return TurnResult(tool_calls=calls, model="m-turn", usage=usage())


def test_the_metered_provider_records_json_calls_with_their_time_and_retries() -> None:
    clock, meter = Clock(), UsageMeter()
    provider = MeteredProvider(Inner(clock, seconds=1.2, retries=2), meter, clock=clock)
    result = provider.complete_json(system="s", user="u", schema=SCHEMA, schema_name="n")
    assert result.content == {"a": 1}  # the answer passes through untouched
    s = meter.summary()
    assert (s.model_calls, s.retries, s.model_ms, s.tool_calls, s.cached_input_tokens) == (1, 2, 1200, 0, 10)
    assert s.models == ("m-json",)


def test_the_metered_provider_counts_the_tool_calls_the_model_asked_for() -> None:
    clock, meter = Clock(), UsageMeter()
    provider = MeteredProvider(Inner(clock), meter, clock=clock)
    turn = provider.complete_turn(system="s", messages=[], tools=[{}], schema=SCHEMA, schema_name="n")
    assert len(turn.tool_calls) == 2
    assert meter.summary().tool_calls == 2 and meter.summary().models == ("m-turn",)


def test_a_failing_provider_call_is_reraised_and_its_retries_and_time_are_kept() -> None:
    clock, meter = Clock(), UsageMeter()
    provider = MeteredProvider(Inner(clock, seconds=3.0, retries=1, error=ProviderError("down", retryable=True)), meter, clock=clock)
    with pytest.raises(ProviderError):
        provider.complete_json(system="s", user="u", schema=SCHEMA, schema_name="n")
    with pytest.raises(ProviderError):
        provider.complete_turn(system="s", messages=[], tools=[], schema=SCHEMA, schema_name="n")
    s = meter.summary()
    assert (s.model_calls, s.retries, s.model_ms) == (0, 2, 6000)


def test_a_retry_noted_before_a_call_is_not_charged_to_it() -> None:
    clock, meter = Clock(), UsageMeter()
    note_retry()  # belongs to some earlier request on this thread
    MeteredProvider(Inner(clock), meter, clock=clock).complete_json(system="s", user="u", schema=SCHEMA, schema_name="n")
    assert meter.summary().retries == 0 and take_retries() == 0


def test_retry_counters_are_per_thread() -> None:
    seen: list[int] = []

    def other() -> None:
        note_retry()
        note_retry()
        seen.append(take_retries())

    note_retry()
    t = threading.Thread(target=other)
    t.start()
    t.join()
    assert seen == [2] and take_retries() == 1


# --- the litellm usage dict ------------------------------------------------------------------------------------------


def test_litellm_usage_surfaces_the_nested_cached_and_reasoning_shares() -> None:
    as_objects = SimpleNamespace(prompt_tokens=100, completion_tokens=30, cost=0.004,
                                 prompt_tokens_details=SimpleNamespace(cached_tokens=64),
                                 completion_tokens_details=SimpleNamespace(reasoning_tokens=12))
    out = _usage_dict(as_objects)
    assert out == {"prompt_tokens": 100, "completion_tokens": 30, "cost": 0.004, "cached_tokens": 64, "reasoning_tokens": 12}
    pydantic_style = SimpleNamespace(model_dump=lambda: {"prompt_tokens": 5, "completion_tokens": 1,
                                                        "prompt_tokens_details": {"cached_tokens": 2},
                                                        "completion_tokens_details": None})
    assert _usage_dict(pydantic_style) == {"prompt_tokens": 5, "completion_tokens": 1, "cached_tokens": 2}


def test_litellm_usage_without_details_is_unchanged() -> None:
    assert _usage_dict(SimpleNamespace(prompt_tokens=11, completion_tokens=7, total_tokens=18)) == {
        "prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}
    assert _usage_dict(None) == {}


def test_the_litellm_provider_notes_an_invalid_output_retry_and_a_fallback_switch(monkeypatch: pytest.MonkeyPatch) -> None:
    import litellm
    from test_litellm_provider import make, reply

    meter = UsageMeter()
    provider, _ = make(monkeypatch, reply("not json at all"), reply('{"a": 1}'))
    result = MeteredProvider(provider, meter).complete_json(system="s", user="u", schema=SCHEMA, schema_name="n")
    assert result.content == {"a": 1}
    one = meter.summary()
    assert one.retries == 1 and one.model_calls == 1  # the unusable first answer is a retry; only a usable answer is a call

    meter = UsageMeter()
    limited = litellm.RateLimitError(message="429", llm_provider="openrouter", model="primary")
    provider, _ = make(monkeypatch, limited, reply('{"a": 1}', model="b/fallback"))
    MeteredProvider(provider, meter).complete_json(system="s", user="u", schema=SCHEMA, schema_name="n")
    two = meter.summary()
    assert two.retries == 1 and two.models == ("b/fallback",) and two.input_tokens == 11 and two.output_tokens == 7


# --- the contract --------------------------------------------------------------------------------------------------


def test_the_example_and_a_real_summary_validate_against_worker_usage() -> None:
    assert not list(validator("worker_usage").iter_errors(example("worker_usage")))
    meter = UsageMeter()
    meter.record_call(usage(cost=0.001), "m", elapsed_s=0.5, retries=0, tool_calls=1)
    dumped = meter.summary().model_dump(mode="json")
    assert not list(validator("worker_usage").iter_errors(dumped))
    assert not list(validator("worker_usage").iter_errors(UsageMeter().summary().model_dump(mode="json")))
    assert UsageSummary.model_validate(example("worker_usage")).model_calls == 3


def test_a_usage_summary_cannot_carry_a_verdict() -> None:
    with pytest.raises(ValueError):
        UsageSummary.model_validate({**example("worker_usage"), "verdict": "pass"})


# --- the endpoints ---------------------------------------------------------------------------------------------------


def _assert_usage(body: dict, *, model_calls: int, tool_calls: int = 0) -> None:
    assert "usage" in body, "the response must carry what the request cost"
    assert not list(validator("worker_usage").iter_errors(body["usage"]))
    assert body["usage"]["model_calls"] == model_calls and body["usage"]["tool_calls"] == tool_calls
    assert body["usage"]["retries"] == 0 and body["usage"]["models"] == [dbl.MODEL]


def test_strategies_returns_what_the_request_cost() -> None:
    response = client(scripted()).post("/v1/strategies", json=sd.strategies_request())
    assert response.status_code == 200, response.text
    body = response.json()
    assert not list(response_validator("/v1/strategies").iter_errors(body))
    # Three planner turns (two of them pulling 3 tools in all) plus the artifact completion.
    _assert_usage(body, model_calls=4, tool_calls=3)


def test_judge_returns_what_the_semantic_evals_cost() -> None:
    response = judge_post(sd.ScriptedStrategyProvider([]))
    assert response.status_code == 200, response.text
    body = response.json()
    assert not list(response_validator("/v1/judge").iter_errors(body))
    ran = [i for i in body["items"] if i["verdict"] != "not_relevant"]
    assert body["usage"]["model_calls"] == len(ran) and len(ran) > 0  # one completion per judged eval
    assert not list(validator("worker_usage").iter_errors(body["usage"]))


def test_revise_returns_what_the_revision_cost() -> None:
    provider = sd.ScriptedStrategyProvider([], {"strategy_revision_v1": revision()})
    response = client(provider).post("/v1/revise", json=revise_request())
    assert response.status_code == 200, response.text
    body = response.json()
    assert not list(response_validator("/v1/revise").iter_errors(body))
    _assert_usage(body, model_calls=1)


def test_human_delta_returns_what_the_labeling_cost() -> None:
    response = delta_client(delta_labeled(["smaller_ask"])).post("/v1/human-delta", json=delta_request())
    assert response.status_code == 200, response.text
    body = response.json()
    assert body["usage"]["model_calls"] == 1 and not list(validator("worker_usage").iter_errors(body["usage"]))


def test_draft_served_from_cassettes_reports_replay_and_no_spend() -> None:
    client_, _ = make_client()
    response = client_.post("/v1/draft", json=sit.ACME_GUIDED.request_copy())
    assert response.status_code == 200, response.text
    body = response.json()
    assert not list(draft_response_validator().iter_errors(body))
    usage_body = body["usage"]
    assert not list(validator("worker_usage").iter_errors(usage_body))
    assert usage_body["usage_source"] == "replay"
    assert usage_body["model_calls"] == 0 and usage_body["input_tokens"] == 0 and usage_body["output_tokens"] == 0
    assert "cost_usd" not in usage_body and usage_body["models"] == []


def test_a_failed_request_returns_the_error_envelope_not_usage() -> None:
    provider = sd.ScriptedStrategyProvider([], {"strategy_revision_v1": revision(to=dbl.people((sd.OUTSIDER, "x")))})
    response = client(provider).post("/v1/revise", json=revise_request())
    assert response.status_code == 502 and "usage" not in response.json()
