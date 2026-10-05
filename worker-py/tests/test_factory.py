from __future__ import annotations

import pytest

from ghost_worker.errors import ConfigError
from ghost_worker.llm.fake_provider import FakeProvider, RecordingProvider
from ghost_worker.llm.factory import build_provider
from ghost_worker.llm.litellm_provider import LiteLLMProvider
from ghost_worker.settings import Settings


def settings(**kw: object) -> Settings:
    return Settings(_env_file=None, **kw)


def test_replay_builds_fake_provider_without_key(tmp_path) -> None:
    provider = build_provider(settings(ghost_llm_mode="replay", cassette_dir=tmp_path))
    assert isinstance(provider, FakeProvider)


def test_live_builds_litellm_provider_with_settings() -> None:
    provider = build_provider(settings(ghost_llm_mode="live", openrouter_api_key="k",
                                       ghost_model="openrouter/a/b", llm_timeout_s=9))
    assert isinstance(provider, LiteLLMProvider)
    assert provider.model == "openrouter/a/b"
    assert provider.timeout_s == 9


def test_record_wraps_litellm_provider(tmp_path) -> None:
    provider = build_provider(settings(ghost_llm_mode="record", openrouter_api_key="k", cassette_dir=tmp_path))
    assert isinstance(provider, RecordingProvider)


@pytest.mark.parametrize("mode", ["live", "record"])
def test_network_modes_require_api_key(mode: str) -> None:
    with pytest.raises(ConfigError):
        build_provider(settings(ghost_llm_mode=mode))


def test_provider_repr_never_contains_key() -> None:
    provider = build_provider(settings(ghost_llm_mode="live", openrouter_api_key="sk-secret-123"))
    assert "sk-secret-123" not in repr(provider)


def test_live_provider_gets_deadline_from_settings() -> None:
    provider = build_provider(settings(ghost_llm_mode="live", openrouter_api_key="k", llm_deadline_s=33))
    assert provider.deadline_s == 33
