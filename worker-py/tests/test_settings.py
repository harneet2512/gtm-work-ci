from __future__ import annotations

import pytest
from pydantic import ValidationError

from ghost_worker.settings import Settings


@pytest.fixture(autouse=True)
def _clean_env(monkeypatch: pytest.MonkeyPatch) -> None:
    for name in ("GHOST_MODEL", "GHOST_FALLBACK_MODEL", "GHOST_LLM_MODE", "CORE_URL",
                 "OPENROUTER_API_KEY", "LLM_TIMEOUT_S", "CASSETTE_DIR"):
        monkeypatch.delenv(name, raising=False)


def test_defaults_match_env_example() -> None:
    s = Settings(_env_file=None)
    assert s.ghost_model == "openrouter/deepseek/deepseek-v4-flash"
    assert s.ghost_llm_mode == "live"
    assert s.core_url == "http://127.0.0.1:8080"


def test_reads_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("GHOST_LLM_MODE", "replay")
    monkeypatch.setenv("GHOST_MODEL", "openrouter/x/y")
    monkeypatch.setenv("CORE_URL", "http://core.internal:9000")
    s = Settings(_env_file=None)
    assert (s.ghost_llm_mode, s.ghost_model, s.core_url) == ("replay", "openrouter/x/y", "http://core.internal:9000")


def test_rejects_unknown_mode() -> None:
    with pytest.raises(ValidationError):
        Settings(_env_file=None, ghost_llm_mode="yolo")


@pytest.mark.parametrize("url", ["ftp://core", "not a url", ""])
def test_rejects_bad_core_url(url: str) -> None:
    with pytest.raises(ValidationError):
        Settings(_env_file=None, core_url=url)


def test_repr_and_dump_redact_api_key() -> None:
    s = Settings(_env_file=None, openrouter_api_key="sk-or-super-secret")
    assert "super-secret" not in repr(s)
    assert "super-secret" not in str(s)
    assert "super-secret" not in str(s.model_dump())
    assert "super-secret" not in s.model_dump_json()
    assert s.api_key_value() == "sk-or-super-secret"


def test_api_key_value_none_when_unset() -> None:
    assert Settings(_env_file=None).api_key_value() is None


def test_settings_are_frozen() -> None:
    with pytest.raises(ValidationError):
        Settings(_env_file=None).ghost_model = "x"  # type: ignore[misc]


def test_blank_fallback_model_means_none(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("GHOST_FALLBACK_MODEL", "")
    assert Settings(_env_file=None).ghost_fallback_model is None


def test_timeout_must_be_positive() -> None:
    with pytest.raises(ValidationError):
        Settings(_env_file=None, llm_timeout_s=0)


def test_concurrency_and_deadline_defaults_and_bounds() -> None:
    s = Settings(_env_file=None)
    assert (s.max_concurrency, s.llm_deadline_s) == (8, 90.0)
    with pytest.raises(ValidationError):
        Settings(_env_file=None, max_concurrency=0)
    with pytest.raises(ValidationError):
        Settings(_env_file=None, llm_deadline_s=0)


def test_draft_limits_have_safe_defaults() -> None:
    s = Settings(_env_file=None)
    assert (s.draft_deadline_s, s.core_timeout_s, s.core_max_bytes) == (120.0, 10.0, 16384)


@pytest.mark.parametrize(("field", "value"), [("draft_deadline_s", 0), ("draft_deadline_s", 901),
                                              ("core_timeout_s", 0), ("core_timeout_s", 61),
                                              ("core_max_bytes", 1023), ("core_max_bytes", 262145)])
def test_draft_limits_are_bounded(field: str, value: float) -> None:
    with pytest.raises(ValidationError):
        Settings(_env_file=None, **{field: value})


def test_draft_limits_read_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("DRAFT_DEADLINE_S", "30")
    monkeypatch.setenv("CORE_MAX_BYTES", "2048")
    s = Settings(_env_file=None)
    assert (s.draft_deadline_s, s.core_max_bytes) == (30.0, 2048)
