"""Build the LLM provider for GHOST_LLM_MODE."""
from __future__ import annotations

from ..errors import ConfigError
from ..settings import Settings
from .cache_provider import CachingProvider, SpendGuard
from .fake_provider import FakeProvider, RecordingProvider, model_family
from .litellm_provider import LiteLLMProvider
from .provider import LLMProvider
from .ratelimit import shared_limiter


def build_provider(settings: Settings, *, deadline_s: float | None = None) -> LLMProvider:
    """Provider for interactive calls; its overall deadline is `llm_deadline_s` unless `deadline_s` is given."""
    family = model_family(settings.ghost_model)
    if settings.ghost_llm_mode == "replay":
        return FakeProvider(settings.cassette_dir, family)

    api_key = settings.api_key_value()
    if settings.ghost_llm_mode == "cache":
        # Replay-first needs no key; record-on-miss needs one (without it a miss is an error, never a silent call).
        live = None
        if api_key:
            live = LiteLLMProvider(model=settings.ghost_model, fallback_model=settings.ghost_fallback_model,
                                   api_key=api_key, timeout_s=settings.llm_timeout_s,
                                   deadline_s=settings.llm_deadline_s if deadline_s is None else deadline_s,
                                   limiter=shared_limiter(settings.ghost_llm_max_rpm),
                                   rate_limit_switch_after=settings.llm_rate_limit_switch_after)
        guard = SpendGuard(api_key, cap_usd=settings.ghost_llm_spend_cap_usd) if api_key else None
        return CachingProvider(live, settings.ghost_llm_cache_dir, family, guard=guard, strict=settings.ghost_llm_cache_strict)
    if not api_key:
        raise ConfigError(f"OPENROUTER_API_KEY is required when GHOST_LLM_MODE={settings.ghost_llm_mode}")
    live = LiteLLMProvider(model=settings.ghost_model, fallback_model=settings.ghost_fallback_model,
                           api_key=api_key, timeout_s=settings.llm_timeout_s,
                           deadline_s=settings.llm_deadline_s if deadline_s is None else deadline_s,
                           limiter=shared_limiter(settings.ghost_llm_max_rpm),
                           rate_limit_switch_after=settings.llm_rate_limit_switch_after)
    if settings.ghost_llm_mode == "record":
        return RecordingProvider(live, settings.cassette_dir, family)
    return live


def build_extract_provider(settings: Settings) -> LLMProvider:
    """Provider for /v1/extract: asynchronous graph maintenance, bounded by `extract_deadline_s`."""
    return build_provider(settings, deadline_s=settings.extract_deadline_s)


def build_judge_provider(settings: Settings) -> LLMProvider:
    """Provider for the semantic judges: offline evaluation, bounded by `judge_deadline_s`."""
    return build_provider(settings, deadline_s=settings.judge_deadline_s)
