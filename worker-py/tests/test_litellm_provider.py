"""LiteLLMProvider with litellm.completion monkeypatched: no network, ever."""
from __future__ import annotations

import json
import threading
import time
from types import SimpleNamespace

import litellm
import pytest

from ghost_worker.errors import InvalidModelOutputError, ProviderError
from ghost_worker.llm.litellm_provider import LiteLLMProvider

SCHEMA = {"type": "object", "properties": {"a": {"type": "integer"}}, "required": ["a"],
          "additionalProperties": False}


def reply(content: str, model: str = "deepseek/deepseek-v4-flash") -> SimpleNamespace:
    return SimpleNamespace(
        choices=[SimpleNamespace(message=SimpleNamespace(content=content))],
        model=model,
        usage=SimpleNamespace(prompt_tokens=11, completion_tokens=7, total_tokens=18),
    )


class Script:
    """Replaces litellm.completion; each call pops the next outcome (value or exception)."""

    def __init__(self, *outcomes: object) -> None:
        self.outcomes = list(outcomes)
        self.calls: list[dict] = []

    def __call__(self, **kwargs: object) -> object:
        self.calls.append(kwargs)
        outcome = self.outcomes.pop(0)
        if callable(outcome):
            outcome = outcome()
        if isinstance(outcome, BaseException):
            raise outcome
        return outcome


def make(monkeypatch: pytest.MonkeyPatch, *outcomes: object, fallback: str | None = "openrouter/b/fallback"):
    script = Script(*outcomes)
    monkeypatch.setattr(litellm, "completion", script)
    provider = LiteLLMProvider(model="openrouter/a/primary", fallback_model=fallback,
                               api_key="sk-test", timeout_s=12.5)
    return provider, script


def call(provider: LiteLLMProvider):
    return provider.complete_json(system="sys", user="usr", schema=SCHEMA, schema_name="thing")


def test_success_returns_parsed_content_model_and_usage(monkeypatch: pytest.MonkeyPatch) -> None:
    provider, script = make(monkeypatch, reply('{"a": 1}'))
    result = call(provider)
    assert result.content == {"a": 1}
    assert result.model == "deepseek/deepseek-v4-flash"
    assert result.usage == {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}
    assert len(script.calls) == 1


def test_request_uses_strict_json_schema_timeout_and_messages(monkeypatch: pytest.MonkeyPatch) -> None:
    provider, script = make(monkeypatch, reply('{"a": 1}'))
    call(provider)
    kw = script.calls[0]
    assert kw["model"] == "openrouter/a/primary"
    assert kw["messages"] == [{"role": "system", "content": "sys"}, {"role": "user", "content": "usr"}]
    assert kw["response_format"] == {"type": "json_schema",
                                     "json_schema": {"name": "thing", "strict": True, "schema": SCHEMA}}
    assert kw["timeout"] == 12.5
    assert kw["api_key"] == "sk-test"
    assert kw["temperature"] == 0
    assert kw["num_retries"] == 0  # litellm must not add hidden retries on top of ours


class Clock:
    def __init__(self) -> None:
        self.now = 1000.0

    def __call__(self) -> float:
        return self.now

    def advance(self, seconds: float):
        def step() -> None:
            self.now += seconds
        return step


def make_with_clock(monkeypatch: pytest.MonkeyPatch, clock: Clock, *outcomes: object, deadline_s: float = 90):
    script = Script(*outcomes)
    monkeypatch.setattr(litellm, "completion", script)
    monkeypatch.setattr("ghost_worker.llm.litellm_provider.time.monotonic", clock)
    provider = LiteLLMProvider(model="openrouter/a/primary", fallback_model="openrouter/b/fallback",
                               api_key="k", timeout_s=60, deadline_s=deadline_s)
    return provider, script


def test_per_call_timeout_is_capped_by_remaining_deadline(monkeypatch: pytest.MonkeyPatch) -> None:
    clock = Clock()

    def slow_then_rate_limited() -> Exception:
        clock.now += 50
        return litellm.RateLimitError(message="429", llm_provider="openrouter", model="primary")

    provider, script = make_with_clock(monkeypatch, clock, slow_then_rate_limited, reply('{"a": 1}'))
    call(provider)
    assert script.calls[0]["timeout"] == 60
    assert script.calls[1]["timeout"] == pytest.approx(40)  # 90s deadline - 50s already spent


def test_deadline_exhausted_aborts_without_another_call(monkeypatch: pytest.MonkeyPatch) -> None:
    clock = Clock()

    def slow_rate_limit() -> Exception:
        clock.now += 95
        return litellm.RateLimitError(message="429", llm_provider="openrouter", model="primary")

    provider, script = make_with_clock(monkeypatch, clock, slow_rate_limit)
    with pytest.raises(ProviderError, match="deadline"):
        call(provider)
    assert len(script.calls) == 1


def test_deadline_applies_to_json_retry(monkeypatch: pytest.MonkeyPatch) -> None:
    clock = Clock()

    def slow_bad_json() -> SimpleNamespace:
        clock.now += 95
        return reply("not json")

    provider, script = make_with_clock(monkeypatch, clock, slow_bad_json)
    with pytest.raises(ProviderError, match="deadline"):
        call(provider)
    assert len(script.calls) == 1


def test_hung_call_is_abandoned_at_the_wall_clock_deadline(monkeypatch: pytest.MonkeyPatch) -> None:
    # OpenRouter keeps idle requests alive with whitespace, so litellm's per-read timeout never fires;
    # the live benchmark saw one call run 2722 s against a 90 s deadline.
    release = threading.Event()

    def hang(**_: object) -> object:
        release.wait(10)
        return reply('{"a": 1}')

    monkeypatch.setattr(litellm, "completion", hang)
    provider = LiteLLMProvider(model="openrouter/a/primary", fallback_model="openrouter/b/fallback",
                               api_key="sk-test", timeout_s=60, deadline_s=0.3)
    started = time.monotonic()
    try:
        with pytest.raises(ProviderError, match="deadline") as caught:
            call(provider)
    finally:
        release.set()
    assert time.monotonic() - started < 2
    assert caught.value.retryable is False


def test_hung_first_attempt_is_retried_within_the_deadline(monkeypatch: pytest.MonkeyPatch) -> None:
    # Live runs: ~9% of calls hang and never answer, while healthy calls finish well inside 60% of the
    # deadline. Abandoning the first attempt early leaves room for one retry instead of failing.
    release = threading.Event()
    calls: list[float] = []

    def hang_then_answer(**_: object) -> object:
        calls.append(time.monotonic())
        if len(calls) == 1:
            release.wait(10)
        return reply('{"a": 1}')

    monkeypatch.setattr(litellm, "completion", hang_then_answer)
    monkeypatch.setattr("ghost_worker.llm.litellm_provider.MIN_RETRY_S", 0.05)
    provider = LiteLLMProvider(model="openrouter/a/primary", fallback_model=None, api_key="sk-test",
                               timeout_s=60, deadline_s=0.5)
    started = time.monotonic()
    try:
        result = call(provider)
    finally:
        release.set()
    assert result.content == {"a": 1}
    assert len(calls) == 2
    assert calls[1] - started == pytest.approx(0.3, abs=0.1)  # first attempt capped at 60% of the deadline
    assert time.monotonic() - started < 0.5


@pytest.mark.parametrize("second", ["hang", "rate_limited"])
def test_retry_failure_is_still_a_classified_provider_error(monkeypatch: pytest.MonkeyPatch, second: str) -> None:
    release = threading.Event()
    calls: list[int] = []

    def hang_then_fail(**_: object) -> object:
        calls.append(1)
        if len(calls) == 1 or second == "hang":
            release.wait(10)
            return reply('{"a": 1}')
        raise litellm.RateLimitError(message="429", llm_provider="openrouter", model="primary")

    monkeypatch.setattr(litellm, "completion", hang_then_fail)
    monkeypatch.setattr("ghost_worker.llm.litellm_provider.MIN_RETRY_S", 0.05)
    provider = LiteLLMProvider(model="openrouter/a/primary", fallback_model=None, api_key="sk-test",
                               timeout_s=60, deadline_s=0.5)
    try:
        with pytest.raises(ProviderError) as caught:
            call(provider)
    finally:
        release.set()
    assert len(calls) == 2
    assert caught.value.retryable is (second == "rate_limited")
    assert ("deadline" in str(caught.value)) is (second == "hang")


def test_no_retry_when_too_little_time_remains(monkeypatch: pytest.MonkeyPatch) -> None:
    release = threading.Event()
    calls: list[float] = []

    def hang(**_: object) -> object:
        calls.append(time.monotonic())
        release.wait(10)
        return reply('{"a": 1}')

    monkeypatch.setattr(litellm, "completion", hang)
    provider = LiteLLMProvider(model="openrouter/a/primary", fallback_model=None, api_key="sk-test",
                               timeout_s=60, deadline_s=0.5)  # 40% left after the first attempt < MIN_RETRY_S
    try:
        with pytest.raises(ProviderError, match="deadline") as caught:
            call(provider)
    finally:
        release.set()
    assert len(calls) == 1
    assert caught.value.retryable is False


def test_litellm_is_imported_lazily_and_offline_flag_is_set() -> None:
    import subprocess
    import sys

    code = ("import os, sys\n"
            "os.environ.pop('LITELLM_LOCAL_MODEL_COST_MAP', None)\n"
            "import ghost_worker.llm.litellm_provider as m\n"
            "assert 'litellm' not in sys.modules, 'litellm imported at module import'\n"
            "assert os.environ['LITELLM_LOCAL_MODEL_COST_MAP'] == 'True'\n")
    from pathlib import Path
    root = Path(__file__).resolve().parents[1]
    done = subprocess.run([sys.executable, "-c", code], cwd=root, capture_output=True, text=True, timeout=120)
    assert done.returncode == 0, done.stderr


def test_one_retry_on_invalid_json_then_success(monkeypatch: pytest.MonkeyPatch) -> None:
    provider, script = make(monkeypatch, reply("not json"), reply('{"a": 2}'))
    assert call(provider).content == {"a": 2}
    assert len(script.calls) == 2
    assert all(c["model"] == "openrouter/a/primary" for c in script.calls)


def test_invalid_json_twice_raises(monkeypatch: pytest.MonkeyPatch) -> None:
    provider, script = make(monkeypatch, reply("nope"), reply("still nope"))
    with pytest.raises(InvalidModelOutputError):
        call(provider)
    assert len(script.calls) == 2


@pytest.mark.parametrize("bad", ["[1, 2]", '"str"', "", "null"])
def test_non_object_json_counts_as_invalid(monkeypatch: pytest.MonkeyPatch, bad: str) -> None:
    provider, _ = make(monkeypatch, reply(bad), reply(bad))
    with pytest.raises(InvalidModelOutputError):
        call(provider)


def test_markdown_fenced_json_is_tolerated(monkeypatch: pytest.MonkeyPatch) -> None:
    provider, script = make(monkeypatch, reply('```json\n{"a": 3}\n```'))
    assert call(provider).content == {"a": 3}
    assert len(script.calls) == 1


def test_missing_choices_is_invalid_output(monkeypatch: pytest.MonkeyPatch) -> None:
    empty = SimpleNamespace(choices=[], model="m", usage=None)
    provider, _ = make(monkeypatch, empty, empty)
    with pytest.raises(InvalidModelOutputError):
        call(provider)


def test_none_content_is_invalid_output(monkeypatch: pytest.MonkeyPatch) -> None:
    bad = SimpleNamespace(choices=[SimpleNamespace(message=SimpleNamespace(content=None))], model="m", usage=None)
    provider, _ = make(monkeypatch, bad, bad)
    with pytest.raises(InvalidModelOutputError):
        call(provider)


def test_usage_missing_yields_empty_dict_and_model_falls_back(monkeypatch: pytest.MonkeyPatch) -> None:
    resp = SimpleNamespace(choices=[SimpleNamespace(message=SimpleNamespace(content='{"a":1}'))])
    provider, _ = make(monkeypatch, resp)
    result = call(provider)
    assert result.usage == {}
    assert result.model == "openrouter/a/primary"


def test_usage_with_model_dump(monkeypatch: pytest.MonkeyPatch) -> None:
    usage = SimpleNamespace(model_dump=lambda: {"prompt_tokens": 1, "weird": None, "x": {"y": 1}})
    resp = SimpleNamespace(choices=[SimpleNamespace(message=SimpleNamespace(content='{"a":1}'))],
                           model="m", usage=usage)
    provider, _ = make(monkeypatch, resp)
    assert call(provider).usage == {"prompt_tokens": 1}


@pytest.mark.parametrize("status", [429, 500, 502, 503])
def test_retryable_status_falls_back(monkeypatch: pytest.MonkeyPatch, status: int) -> None:
    err = litellm.APIError(status_code=status, message="boom", llm_provider="openrouter", model="primary")
    provider, script = make(monkeypatch, err, reply('{"a": 9}', model="fallback-m"))
    result = call(provider)
    assert result.content == {"a": 9}
    assert result.model == "fallback-m"
    assert [c["model"] for c in script.calls] == ["openrouter/a/primary", "openrouter/b/fallback"]


def test_rate_limit_and_timeout_exceptions_fall_back(monkeypatch: pytest.MonkeyPatch) -> None:
    rl = litellm.RateLimitError(message="slow down", llm_provider="openrouter", model="primary")
    provider, script = make(monkeypatch, rl, reply('{"a": 1}'))
    assert call(provider).content == {"a": 1}
    to = litellm.Timeout(message="t", model="primary", llm_provider="openrouter")
    provider, script = make(monkeypatch, to, reply('{"a": 1}'))
    assert call(provider).content == {"a": 1}
    assert len(script.calls) == 2


def test_connection_error_falls_back(monkeypatch: pytest.MonkeyPatch) -> None:
    err = litellm.APIConnectionError(message="dns", llm_provider="openrouter", model="primary")
    provider, script = make(monkeypatch, err, reply('{"a": 1}'))
    assert call(provider).content == {"a": 1}
    assert len(script.calls) == 2


def test_fallback_also_failing_raises_provider_error(monkeypatch: pytest.MonkeyPatch) -> None:
    e = litellm.APIError(status_code=503, message="down", llm_provider="openrouter", model="m")
    provider, script = make(monkeypatch, e, e)
    with pytest.raises(ProviderError):
        call(provider)
    assert len(script.calls) == 2


def test_no_fallback_configured_raises(monkeypatch: pytest.MonkeyPatch) -> None:
    e = litellm.APIError(status_code=503, message="down", llm_provider="openrouter", model="m")
    provider, script = make(monkeypatch, e, fallback=None)
    with pytest.raises(ProviderError):
        call(provider)
    assert len(script.calls) == 1


@pytest.mark.parametrize("status", [400, 401, 403, 404])
def test_non_retryable_status_does_not_fall_back(monkeypatch: pytest.MonkeyPatch, status: int) -> None:
    e = litellm.APIError(status_code=status, message="no", llm_provider="openrouter", model="m")
    provider, script = make(monkeypatch, e)
    with pytest.raises(ProviderError) as info:
        call(provider)
    assert not info.value.retryable
    assert len(script.calls) == 1


def test_unexpected_exception_is_wrapped_without_fallback(monkeypatch: pytest.MonkeyPatch) -> None:
    provider, script = make(monkeypatch, RuntimeError("kaboom"))
    with pytest.raises(ProviderError):
        call(provider)
    assert len(script.calls) == 1


def test_error_message_does_not_contain_api_key(monkeypatch: pytest.MonkeyPatch) -> None:
    e = litellm.APIError(status_code=401, message="bad key sk-test", llm_provider="openrouter", model="m")
    provider, _ = make(monkeypatch, e)
    with pytest.raises(ProviderError) as info:
        call(provider)
    assert "sk-test" not in str(info.value)


def test_result_content_is_json_serialisable(monkeypatch: pytest.MonkeyPatch) -> None:
    provider, _ = make(monkeypatch, reply('{"a": 5}'))
    assert json.dumps(call(provider).content) == '{"a": 5}'
