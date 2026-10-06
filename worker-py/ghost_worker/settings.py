"""Environment-driven settings. Secrets are SecretStr so repr/dumps never leak them."""
from __future__ import annotations

from pathlib import Path
from typing import Annotated, Literal
from urllib.parse import urlparse

from pydantic import BeforeValidator, Field, SecretStr, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

LLMMode = Literal["live", "record", "replay", "cache"]
DEFAULT_CASSETTE_DIR = Path(__file__).resolve().parents[1] / "cassettes"


def _blank_to_none(value: object) -> object:
    return None if isinstance(value, str) and not value.strip() else value


class Settings(BaseSettings):
    """Worker configuration. `core_url` comes only from the environment, never from a request."""

    model_config = SettingsConfigDict(env_file=None, extra="ignore", frozen=True, case_sensitive=False)

    ghost_model: str = "openrouter/deepseek/deepseek-v4-flash"
    ghost_fallback_model: Annotated[str | None, BeforeValidator(_blank_to_none)] = (
        "openrouter/dots-studio/dots-3-note-preview:free")
    ghost_llm_mode: LLMMode = "live"
    openrouter_api_key: Annotated[SecretStr | None, BeforeValidator(_blank_to_none)] = None
    core_url: str = "http://127.0.0.1:8080"
    llm_timeout_s: float = Field(default=60.0, gt=0, le=600)
    llm_deadline_s: float = Field(default=90.0, gt=0, le=900)  # interactive provider calls (/v1/draft)
    # /v1/extract is asynchronous graph maintenance (no user waiting): long transcripts get a longer budget.
    extract_deadline_s: float = Field(default=180.0, gt=0, le=900)
    # Semantic judges (HAR-116) are offline evaluation with long structured answers; no user waits on one call.
    judge_deadline_s: float = Field(default=180.0, gt=0, le=900)
    draft_deadline_s: float = Field(default=120.0, gt=0, le=900)
    # /v1/strategies plans (with pulls) and drafts three artifacts: roughly two to three /v1/draft calls.
    strategies_deadline_s: float = Field(default=300.0, gt=0, le=900)
    core_timeout_s: float = Field(default=10.0, gt=0, le=60)
    core_max_bytes: int = Field(default=16384, ge=1024, le=262144)
    max_concurrency: int = Field(default=8, ge=1, le=64)
    # Provider circuit breaker (HAR-135): consecutive non-retryable provider failures that pause model calls,
    # and how long they stay paused before one call is allowed through again.
    # Process-wide request pacing (requests per minute, shared by extract, judge and draft calls). Unset =
    # unlimited. OpenRouter `:free` models allow ~20/min: use 15. A 429 backs off (Retry-After, else exponential
    # with jitter) and the fallback model is used after `llm_rate_limit_switch_after` consecutive 429s.
    ghost_llm_max_rpm: Annotated[float | None, BeforeValidator(_blank_to_none)] = Field(default=None, gt=0, le=100000)
    llm_rate_limit_switch_after: int = Field(default=3, ge=1, le=20)
    provider_breaker_threshold: int = Field(default=5, ge=1, le=1000)
    provider_breaker_cooldown_s: float = Field(default=300.0, gt=0, le=86400)
    cassette_dir: Path = DEFAULT_CASSETTE_DIR
    # GHOST_LLM_MODE=cache (the demo default): replay-first, record-on-miss, once. The cache lives outside the repo.
    ghost_llm_cache_dir: Path = DEFAULT_CASSETTE_DIR.parent / ".llm-cache"
    ghost_llm_cache_strict: bool = False  # a miss is an error: proves a rehearsal is fully recorded
    ghost_llm_spend_cap_usd: float = Field(default=7.5, gt=0, le=1000)  # stop recording past this much total key spend

    @field_validator("core_url")
    @classmethod
    def _http_url(cls, value: str) -> str:
        parsed = urlparse(value)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc:
            raise ValueError("core_url must be an absolute http(s) URL")
        return value.rstrip("/")

    def api_key_value(self) -> str | None:
        """The only accessor for the raw key; call it at the provider boundary, never log the result."""
        return self.openrouter_api_key.get_secret_value() if self.openrouter_api_key else None
