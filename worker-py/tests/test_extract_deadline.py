"""Extraction is asynchronous graph maintenance: it gets its own provider deadline (extract_deadline_s),
while draft calls keep the interactive llm_deadline_s. litellm.completion and the clock are faked."""
from __future__ import annotations

import time
from pathlib import Path

import litellm
import pytest
from pydantic import ValidationError

from ghost_worker.app import create_app
from ghost_worker.errors import ProviderError
from ghost_worker.llm.factory import build_extract_provider, build_provider
from ghost_worker.settings import Settings
from test_litellm_provider import SCHEMA, Clock, Script, reply

LONG_CALL_S = 100.0  # longer than the 90 s interactive deadline, shorter than the 180 s extraction default


def live_settings(**kw: object) -> Settings:
    return Settings(_env_file=None, ghost_llm_mode="live", openrouter_api_key="k", **kw)


def slow_invalid_then_valid(monkeypatch: pytest.MonkeyPatch) -> Script:
    """First answer is unusable and takes LONG_CALL_S (fake clock); the JSON retry then needs time left."""
    clock = Clock()

    def slow_invalid() -> object:
        clock.now += LONG_CALL_S
        return reply("not json")

    script = Script(slow_invalid, reply('{"a": 1}'))
    monkeypatch.setattr(litellm, "completion", script)
    monkeypatch.setattr("ghost_worker.llm.litellm_provider.time.monotonic", clock)
    return script


def test_extract_deadline_defaults_to_180_and_is_bounded() -> None:
    assert Settings(_env_file=None).extract_deadline_s == 180.0
    assert Settings(_env_file=None, extract_deadline_s=900).extract_deadline_s == 900
    for bad in (0, 901):
        with pytest.raises(ValidationError):
            Settings(_env_file=None, extract_deadline_s=bad)


def test_env_example_documents_the_deadline_defaults(monkeypatch: pytest.MonkeyPatch) -> None:
    lines = (Path(__file__).resolve().parents[2] / ".env.example").read_text(encoding="utf-8").splitlines()
    env = dict(line.split("=", 1) for line in lines if "=" in line and not line.startswith("#"))
    defaults = Settings(_env_file=None)
    assert float(env["EXTRACT_DEADLINE_S"]) == defaults.extract_deadline_s
    assert float(env["LLM_DEADLINE_S"]) == defaults.llm_deadline_s
    monkeypatch.setenv("EXTRACT_DEADLINE_S", "300")
    assert Settings(_env_file=None).extract_deadline_s == 300


def test_extract_provider_uses_the_extract_deadline_and_draft_keeps_the_llm_deadline() -> None:
    cfg = live_settings(extract_deadline_s=240, llm_deadline_s=45)
    assert build_extract_provider(cfg).deadline_s == 240
    assert build_provider(cfg).deadline_s == 45


def test_extract_call_may_run_past_90_s(monkeypatch: pytest.MonkeyPatch) -> None:
    script = slow_invalid_then_valid(monkeypatch)
    provider = build_extract_provider(live_settings())
    result = provider.complete_json(system="s", user="u", schema=SCHEMA, schema_name="thing")
    assert result.content == {"a": 1} and len(script.calls) == 2


def test_draft_call_is_still_bounded_at_90_s(monkeypatch: pytest.MonkeyPatch) -> None:
    script = slow_invalid_then_valid(monkeypatch)
    provider = build_provider(live_settings())
    with pytest.raises(ProviderError, match="deadline"):
        provider.complete_turn(system="s", messages=[], tools=[], schema=SCHEMA, schema_name="thing")
    assert len(script.calls) == 1


def test_app_wires_extract_and_draft_deadlines_and_one_shared_cap() -> None:
    app = create_app(settings=live_settings(max_concurrency=3))
    extract, draft = app.state.extract_provider, app.state.draft_provider
    assert extract.inner.inner.deadline_s == 180 and draft.inner.inner.deadline_s == 90
    assert extract.acquire_timeout_s <= 180 and draft.acquire_timeout_s == 90
    assert extract.shares_slots_with(draft)


def test_hang_cutoff_is_a_share_of_the_extract_deadline_not_the_draft_one(monkeypatch: pytest.MonkeyPatch) -> None:
    """PR #14 abandons a hung first attempt at FIRST_ATTEMPT_SHARE of the remaining deadline: for /v1/extract that
    share comes from extract_deadline_s, so a 0.5 s call survives there but is cut on the 0.2 s draft provider."""
    def slow(**_: object) -> object:
        time.sleep(0.5)
        return reply('{"a": 1}')

    monkeypatch.setattr(litellm, "completion", slow)
    cfg = live_settings(extract_deadline_s=2.0, llm_deadline_s=0.2)
    kw = {"system": "s", "user": "u", "schema": SCHEMA, "schema_name": "thing"}
    assert build_extract_provider(cfg).complete_json(**kw).content == {"a": 1}
    with pytest.raises(ProviderError, match="deadline"):
        build_provider(cfg).complete_json(**kw)
