"""litellm/OpenRouter provider: strict json_schema output, one JSON retry, fallback model on 429/5xx.

litellm is imported lazily (first real call) so replay mode and tests never import it or touch the network.
A single overall deadline bounds the worst case across primary, JSON retry and fallback.
"""
from __future__ import annotations

import json
import logging
import random
import re
import threading
import time
from collections.abc import Callable
from typing import Any, TypeVar

from ..errors import (DailyCapError, InvalidModelOutputError, ProviderError, ProviderUnavailableError,
                      RateLimitedError)
from .classify import (is_daily_cap, is_nonretryable_provider_failure, retry_after_seconds,
                       seconds_until_utc_midnight)
from .ratelimit import RateLimiter
from .provider import LLMResult, ToolCall, TurnResult

log = logging.getLogger(__name__)

T = TypeVar("T")

_FENCE = re.compile(r"^```(?:json)?\s*(.*?)\s*```$", re.DOTALL)


def _is_retryable(exc: BaseException) -> bool:
    import litellm  # already imported by the failed call; kept lazy for module-import hygiene

    if isinstance(exc, (litellm.Timeout, litellm.APIConnectionError, litellm.RateLimitError)):
        return True
    status = getattr(exc, "status_code", None)
    return isinstance(status, int) and (status == 429 or status >= 500)


# A hung call never answers. Healthy calls finish well inside 60% of the deadline (live extraction:
# p90 61 s, max 107 s of 180 s), so the first attempt is abandoned there and one retry gets the rest,
# provided at least MIN_RETRY_S remain.
FIRST_ATTEMPT_SHARE = 0.6
MIN_RETRY_S = 5.0


class _WallClockExceeded(Exception):
    pass


def _classified(exc: Exception, model: str) -> ProviderError:
    import litellm

    status = getattr(exc, "status_code", None)
    if is_daily_cap(exc):
        return DailyCapError(f"daily free-model request cap reached ({model}); model calls paused until 00:00 UTC",
                             resume_in_s=seconds_until_utc_midnight())
    if is_nonretryable_provider_failure(exc):
        return ProviderUnavailableError(f"{type(exc).__name__} from {model} (status={status})")
    if status == 429 or isinstance(exc, litellm.RateLimitError):
        return RateLimitedError(f"{type(exc).__name__} from {model} (status=429)", retry_after_s=retry_after_seconds(exc))
    return ProviderError(f"{type(exc).__name__} from {model} (status={status})", retryable=_is_retryable(exc))


def _within(fn: Callable[[], T], seconds: float) -> T:
    """Run fn with a wall-clock bound. litellm's timeout is per read, and OpenRouter keeps idle requests
    alive with whitespace, so a stuck call can outlive it indefinitely. The abandoned call finishes on a
    daemon thread (never joined at interpreter exit) and its result is discarded."""
    box: dict[str, Any] = {}

    def run() -> None:
        try:
            box["value"] = fn()
        except BaseException as exc:  # noqa: BLE001 - re-raised in the caller's thread
            box["error"] = exc

    worker = threading.Thread(target=run, name="llm-call", daemon=True)
    worker.start()
    worker.join(seconds)
    if worker.is_alive():
        raise _WallClockExceeded
    if "error" in box:
        raise box["error"]
    return box["value"]


def _parse_object(raw: object) -> dict[str, Any]:
    if not isinstance(raw, str) or not raw.strip():
        raise InvalidModelOutputError("model returned no text content")
    text = raw.strip()
    fenced = _FENCE.match(text)
    if fenced:
        text = fenced.group(1)
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError as exc:
        raise InvalidModelOutputError("model output is not valid JSON") from exc
    if not isinstance(parsed, dict):
        raise InvalidModelOutputError("model output is not a JSON object")
    return parsed


def _usage_dict(usage: object) -> dict[str, int | float]:
    if usage is None:
        return {}
    raw = usage.model_dump() if hasattr(usage, "model_dump") else getattr(usage, "__dict__", usage)
    if not isinstance(raw, dict):
        return {}
    return {k: v for k, v in raw.items() if isinstance(v, (int, float)) and not isinstance(v, bool)}


def _json_schema_format(schema_name: str, schema: dict[str, Any]) -> dict[str, Any]:
    return {"type": "json_schema", "json_schema": {"name": schema_name, "strict": True, "schema": schema}}


def _tool_call(raw: object) -> ToolCall:
    function = getattr(raw, "function", None)
    raw_arguments = getattr(function, "arguments", None)
    arguments = _parse_object("{}" if raw_arguments is None else raw_arguments)
    return ToolCall(id=str(getattr(raw, "id", "")), name=str(getattr(function, "name", "")), arguments=arguments)


class LiteLLMProvider:
    def __init__(self, *, model: str, fallback_model: str | None, api_key: str | None, timeout_s: float,
                 deadline_s: float = 90.0, limiter: RateLimiter | None = None, rate_limit_switch_after: int = 1,
                 backoff_base_s: float = 1.0, backoff_cap_s: float = 30.0,
                 sleep: Callable[[float], None] = time.sleep, rng: Callable[[], float] = random.random) -> None:
        self.model = model
        self._limiter = limiter
        self.rate_limit_switch_after = max(1, rate_limit_switch_after)  # consecutive 429s before the fallback model
        self._backoff_base_s, self._backoff_cap_s = backoff_base_s, backoff_cap_s
        self._sleep, self._rng = sleep, rng
        self.fallback_model = fallback_model
        self.timeout_s = timeout_s
        self.deadline_s = deadline_s
        self._api_key = api_key

    def __repr__(self) -> str:
        return f"LiteLLMProvider(model={self.model!r}, fallback_model={self.fallback_model!r})"

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        def attempt(model: str, deadline: float) -> LLMResult:
            return self._call(model, system, user, schema, schema_name, deadline)

        return self._guarded(attempt)

    def complete_turn(self, *, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema: dict[str, Any], schema_name: str) -> TurnResult:
        def attempt(model: str, deadline: float) -> TurnResult:
            return self._call_turn(model, system, messages, tools, schema, schema_name, deadline)

        return self._guarded(attempt)

    def _guarded(self, attempt: Callable[[str, float], T]) -> T:
        """Overall deadline, one retry on unusable output, fallback model on retryable provider errors."""
        deadline = time.monotonic() + self.deadline_s
        try:
            return self._with_json_retry(attempt, self.model, deadline)
        except ProviderError as exc:
            if not (exc.retryable and self.fallback_model):
                raise
            log.warning("primary model failed, using fallback", extra={"model": self.model,
                                                                      "fallback_model": self.fallback_model})
            return self._with_json_retry(attempt, self.fallback_model, deadline)

    @staticmethod
    def _with_json_retry(attempt: Callable[[str, float], T], model: str, deadline: float) -> T:
        try:
            return attempt(model, deadline)
        except InvalidModelOutputError:
            log.warning("invalid JSON from model, retrying once", extra={"model": model})
            return attempt(model, deadline)

    def _call(self, model: str, system: str, user: str, schema: dict[str, Any], schema_name: str,
              deadline: float) -> LLMResult:
        response = self._completion(
            model, deadline, messages=[{"role": "system", "content": system}, {"role": "user", "content": user}],
            response_format=_json_schema_format(schema_name, schema))
        content = _parse_object(self._message_content(response))
        return LLMResult(content=content, model=getattr(response, "model", None) or model,
                         usage=_usage_dict(getattr(response, "usage", None)))

    def _call_turn(self, model: str, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                   schema: dict[str, Any], schema_name: str, deadline: float) -> TurnResult:
        # Tools and a strict response_format are not combinable on every route: offer the tools while the
        # agent may still pull, and force the schema only once none are left.
        extra: dict[str, Any] = {"tools": tools} if tools else {"response_format": _json_schema_format(schema_name, schema)}
        response = self._completion(model, deadline, messages=[{"role": "system", "content": system}, *messages],
                                    **extra)
        message = self._message(response)
        calls = tuple(_tool_call(raw) for raw in (getattr(message, "tool_calls", None) or ()))
        resolved = getattr(response, "model", None) or model
        usage = _usage_dict(getattr(response, "usage", None))
        if calls:
            return TurnResult(tool_calls=calls, model=resolved, usage=usage)
        return TurnResult(content=_parse_object(getattr(message, "content", None)), model=resolved, usage=usage)

    def _completion(self, model: str, deadline: float, **request: Any) -> object:
        """One paced call; a 429 backs off (Retry-After, else exponential with jitter) and retries until
        `rate_limit_switch_after` consecutive 429s or the deadline, then raises so `_guarded` can use the fallback."""
        hits = 0
        while True:
            if self._limiter is not None:
                self._limiter.acquire(max(0.0, deadline - time.monotonic()))
            try:
                return self._completion_once(model, deadline, **request)
            except RateLimitedError as exc:
                hits += 1
                delay = self._rate_limit_delay(exc, hits)
                if hits >= self.rate_limit_switch_after or delay >= deadline - time.monotonic():
                    raise
                log.warning("rate limited, backing off", extra={"model": model, "attempt": hits, "delay_s": delay})
                self._sleep(delay)

    def _rate_limit_delay(self, exc: RateLimitedError, hits: int) -> float:
        if exc.retry_after_s is not None:
            return exc.retry_after_s
        ceiling = min(self._backoff_cap_s, self._backoff_base_s * 2 ** (hits - 1))
        return ceiling * (0.5 + self._rng() / 2)  # jitter: 50-100% of the exponential step

    def _completion_once(self, model: str, deadline: float, **request: Any) -> object:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise ProviderError("overall deadline exceeded", retryable=False)
        import litellm

        def send(bound: float) -> Callable[[], object]:
            return lambda: litellm.completion(model=model, timeout=min(self.timeout_s, bound), api_key=self._api_key,
                                              temperature=0, num_retries=0, **request)

        try:
            return _within(send(remaining), remaining * FIRST_ATTEMPT_SHARE)
        except _WallClockExceeded:
            retry_bound = deadline - time.monotonic()
            if retry_bound < MIN_RETRY_S:
                raise ProviderError(f"overall deadline exceeded waiting for {model}", retryable=False) from None
            log.warning("model call hung, retrying once", extra={"model": model, "retry_bound_s": retry_bound})
            return self._retry_hung(send(retry_bound), retry_bound, model)
        except Exception as exc:  # noqa: BLE001 - every upstream failure is classified, never leaked
            raise _classified(exc, model) from None

    @staticmethod
    def _retry_hung(attempt: Callable[[], object], bound: float, model: str) -> object:
        try:
            return _within(attempt, bound)
        except _WallClockExceeded:
            raise ProviderError(f"overall deadline exceeded waiting for {model}", retryable=False) from None
        except Exception as exc:  # noqa: BLE001 - every upstream failure is classified, never leaked
            raise _classified(exc, model) from None

    @staticmethod
    def _message(response: object) -> object:
        choices = getattr(response, "choices", None)
        if not choices:
            raise InvalidModelOutputError("model response has no choices")
        return getattr(choices[0], "message", None)

    @classmethod
    def _message_content(cls, response: object) -> object:
        return getattr(cls._message(response), "content", None)
