"""complete_turn: the cassette-able tool-calling step on every provider (fake, recording, limited, litellm)."""
from __future__ import annotations

import json
from pathlib import Path
from types import SimpleNamespace

import litellm
import pytest
from pydantic import ValidationError

from ghost_worker.errors import CassetteNotFoundError, InvalidModelOutputError, ProviderError
from ghost_worker.llm.fake_provider import FakeProvider, RecordingProvider, turn_cassette_key
from ghost_worker.llm.limited import ConcurrencyLimitedProvider
from ghost_worker.llm.litellm_provider import LiteLLMProvider
from ghost_worker.llm.provider import ToolCall, TurnResult

TOOLS = [{"type": "function", "function": {"name": "people", "description": "d",
                                           "parameters": {"type": "object", "properties": {}}}}]
SCHEMA = {"type": "object", "properties": {"a": {"type": "integer"}}}
MESSAGES = [{"role": "user", "content": "hello"}]
KW = {"system": "sys", "messages": MESSAGES, "tools": TOOLS, "schema": SCHEMA, "schema_name": "decision"}
CALL = ToolCall(id="call_1", name="people", arguments={"limit": 5})


def test_turn_result_needs_exactly_one_of_content_or_tool_calls() -> None:
    assert TurnResult(content={"a": 1}, model="m").tool_calls == ()
    assert TurnResult(tool_calls=(CALL,), model="m").content is None
    with pytest.raises(ValidationError):
        TurnResult(model="m")
    with pytest.raises(ValidationError):
        TurnResult(content={"a": 1}, tool_calls=(CALL,), model="m")


def test_frozen_results_do_not_expose_mutable_mappings() -> None:
    turn = TurnResult(tool_calls=(ToolCall(id="c", name="people", arguments={"limit": 5}),), model="m",
                      usage={"total_tokens": 3})
    final = TurnResult(content={"a": 1}, model="m")
    for mapping in (turn.tool_calls[0].arguments, turn.usage, final.content):
        with pytest.raises(TypeError):
            mapping["x"] = 1  # type: ignore[index]
    assert turn.tool_calls[0].arguments["limit"] == 5 and dict(final.content) == {"a": 1}
    assert turn.model_dump(mode="json")["tool_calls"][0]["arguments"] == {"limit": 5}
    arguments = {"limit": 5}
    call = ToolCall(id="c", name="people", arguments=arguments)
    arguments["limit"] = 99  # mutating the caller's dict afterwards cannot change the frozen call
    assert call.arguments["limit"] == 5


def test_turn_key_depends_on_every_input() -> None:
    base = turn_cassette_key("fam", "sys", MESSAGES, TOOLS, "decision", SCHEMA)
    assert base == turn_cassette_key("fam", "sys", list(MESSAGES), list(TOOLS), "decision", dict(SCHEMA))
    variants = [
        turn_cassette_key("other", "sys", MESSAGES, TOOLS, "decision", SCHEMA),
        turn_cassette_key("fam", "sys2", MESSAGES, TOOLS, "decision", SCHEMA),
        turn_cassette_key("fam", "sys", [*MESSAGES, {"role": "user", "content": "x"}], TOOLS, "decision", SCHEMA),
        turn_cassette_key("fam", "sys", MESSAGES, [], "decision", SCHEMA),
        turn_cassette_key("fam", "sys", MESSAGES, TOOLS, "other", SCHEMA),
        turn_cassette_key("fam", "sys", MESSAGES, TOOLS, "decision", {"type": "object"}),
    ]
    assert len({base, *variants}) == 7


class Upstream:
    def __init__(self, result: TurnResult) -> None:
        self.result = result
        self.calls: list[dict] = []

    def complete_turn(self, **kw: object) -> TurnResult:
        self.calls.append(kw)
        return self.result


def test_record_then_replay_round_trip(tmp_path: Path) -> None:
    result = TurnResult(tool_calls=(CALL,), model="deepseek/x", usage={"total_tokens": 3})
    RecordingProvider(Upstream(result), tmp_path, "fam").complete_turn(**KW)
    key = turn_cassette_key("fam", "sys", MESSAGES, TOOLS, "decision", SCHEMA)
    assert json.loads((tmp_path / f"{key}.json").read_text(encoding="utf-8"))["key"] == key
    assert FakeProvider(tmp_path, "fam").complete_turn(**KW) == result


def test_replay_missing_cassette_never_touches_network(tmp_path: Path) -> None:
    with pytest.raises(CassetteNotFoundError, match="decision"):
        FakeProvider(tmp_path, "fam").complete_turn(**KW)


def test_replay_corrupt_turn_cassette_raises(tmp_path: Path) -> None:
    key = turn_cassette_key("fam", "sys", MESSAGES, TOOLS, "decision", SCHEMA)
    (tmp_path / f"{key}.json").write_text(json.dumps({"response": {"model": "m"}}), encoding="utf-8")
    with pytest.raises(InvalidModelOutputError):
        FakeProvider(tmp_path, "fam").complete_turn(**KW)


def test_limited_provider_delegates_turns_and_enforces_cap() -> None:
    result = TurnResult(content={"a": 1}, model="m")
    limited = ConcurrencyLimitedProvider(Upstream(result), max_concurrent=1, acquire_timeout_s=0.05)
    assert limited.complete_turn(**KW) == result
    assert limited._slots.acquire(blocking=False)  # slot released after the call
    with pytest.raises(ProviderError, match="busy"):
        limited.complete_turn(**KW)


# --- litellm -----------------------------------------------------------------------------------

def tool_reply(*calls: tuple[str, str, str], content: str | None = None) -> SimpleNamespace:
    tool_calls = [SimpleNamespace(id=i, function=SimpleNamespace(name=n, arguments=a)) for i, n, a in calls]
    message = SimpleNamespace(content=content, tool_calls=tool_calls or None)
    return SimpleNamespace(choices=[SimpleNamespace(message=message)], model="m-actual",
                           usage=SimpleNamespace(prompt_tokens=5, completion_tokens=2))


class Script:
    def __init__(self, *outcomes: object) -> None:
        self.outcomes = list(outcomes)
        self.calls: list[dict] = []

    def __call__(self, **kwargs: object) -> object:
        self.calls.append(kwargs)
        outcome = self.outcomes.pop(0)
        if isinstance(outcome, BaseException):
            raise outcome
        return outcome


def provider(monkeypatch: pytest.MonkeyPatch, *outcomes: object, fallback: str | None = "openrouter/b/fb"):
    script = Script(*outcomes)
    monkeypatch.setattr(litellm, "completion", script)
    return LiteLLMProvider(model="openrouter/a/p", fallback_model=fallback, api_key="k", timeout_s=9), script


def test_litellm_turn_returns_parsed_tool_calls(monkeypatch: pytest.MonkeyPatch) -> None:
    llm, script = provider(monkeypatch, tool_reply(("c1", "people", '{"limit": 3}')))
    result = llm.complete_turn(**KW)
    assert result.tool_calls == (ToolCall(id="c1", name="people", arguments={"limit": 3}),)
    assert result.model == "m-actual" and result.usage == {"prompt_tokens": 5, "completion_tokens": 2}
    sent = script.calls[0]
    assert sent["tools"] == TOOLS and "response_format" not in sent
    assert sent["messages"][0] == {"role": "system", "content": "sys"} and sent["messages"][1:] == MESSAGES
    assert sent["temperature"] == 0 and sent["num_retries"] == 0


def test_litellm_turn_without_tools_forces_the_json_schema(monkeypatch: pytest.MonkeyPatch) -> None:
    llm, script = provider(monkeypatch, tool_reply(content='{"a": 1}'))
    result = llm.complete_turn(**{**KW, "tools": []})
    assert result.content == {"a": 1}
    sent = script.calls[0]
    assert "tools" not in sent
    assert sent["response_format"]["json_schema"] == {"name": "decision", "strict": True, "schema": SCHEMA}


def test_litellm_turn_final_content_with_tools_offered(monkeypatch: pytest.MonkeyPatch) -> None:
    llm, _ = provider(monkeypatch, tool_reply(content='```json\n{"a": 2}\n```'))
    assert llm.complete_turn(**KW).content == {"a": 2}


@pytest.mark.parametrize("arguments", ["not json", "[1]", ""])
def test_litellm_turn_bad_tool_arguments_retry_once_then_raise(monkeypatch: pytest.MonkeyPatch, arguments: str) -> None:
    llm, script = provider(monkeypatch, tool_reply(("c1", "people", arguments)),
                           tool_reply(("c1", "people", arguments)))
    with pytest.raises(InvalidModelOutputError):
        llm.complete_turn(**KW)
    assert len(script.calls) == 2


def test_litellm_turn_empty_arguments_object_ok(monkeypatch: pytest.MonkeyPatch) -> None:
    llm, _ = provider(monkeypatch, tool_reply(("c1", "people", "{}")))
    assert llm.complete_turn(**KW).tool_calls[0].arguments == {}


def test_litellm_turn_falls_back_on_retryable_provider_error(monkeypatch: pytest.MonkeyPatch) -> None:
    rate = litellm.RateLimitError(message="429", llm_provider="openrouter", model="p")
    llm, script = provider(monkeypatch, rate, tool_reply(("c1", "people", "{}")))
    assert llm.complete_turn(**KW).tool_calls
    assert [c["model"] for c in script.calls] == ["openrouter/a/p", "openrouter/b/fb"]


def test_litellm_turn_non_retryable_error_is_provider_error(monkeypatch: pytest.MonkeyPatch) -> None:
    llm, script = provider(monkeypatch, ValueError("boom"))
    with pytest.raises(ProviderError):
        llm.complete_turn(**KW)
    assert len(script.calls) == 1


def test_litellm_turn_without_choices_is_invalid(monkeypatch: pytest.MonkeyPatch) -> None:
    empty = SimpleNamespace(choices=[], model="m", usage=None)
    llm, _ = provider(monkeypatch, empty, empty)
    with pytest.raises(InvalidModelOutputError):
        llm.complete_turn(**KW)
