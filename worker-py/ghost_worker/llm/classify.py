"""Which upstream failures can never succeed on retry (HAR-135)."""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

NONRETRYABLE_STATUSES = frozenset({401, 402, 403})

# Matched case-insensitively against the exception text; the text is never logged or returned.
NONRETRYABLE_MARKERS = (
    "insufficient credit", "insufficient_credit", "insufficient funds", "insufficient_quota", "payment required",
    "out of credits", "quota", "invalid api key", "incorrect api key", "invalid_api_key", "authentication",
    "unauthorized", "forbidden", "permission denied",
)


def is_nonretryable_provider_failure(exc: BaseException) -> bool:
    status = getattr(exc, "status_code", None)
    if isinstance(status, int) and status in NONRETRYABLE_STATUSES:
        return True
    text = str(exc).lower()
    if "insufficient_quota" in text:  # explicit: OpenAI-style "no quota left", sent as a 429
        return True
    # The generic words ("quota", "authentication", ...) are trusted only where a retry cannot be the answer:
    # no status at all, or a 4xx other than 429. A 429 or a 5xx mentioning "quota" is ordinary throttling.
    textual = status is None or (isinstance(status, int) and 400 <= status < 500 and status != 429)
    return textual and any(marker in text for marker in NONRETRYABLE_MARKERS)


DAILY_CAP_MARKERS = ("free-models-per-day", "free models per day", "per-day", "per day", "daily limit", "daily cap")


def is_daily_cap(exc: BaseException) -> bool:
    """OpenRouter answers 429 with a free-models-per-day message once the daily free quota is spent."""
    status = getattr(exc, "status_code", None)
    text = str(exc).lower()
    return (status == 429 or "ratelimit" in type(exc).__name__.lower()) and any(m in text for m in DAILY_CAP_MARKERS)


def seconds_until_utc_midnight(now: datetime | None = None) -> float:
    now = now or datetime.now(timezone.utc)
    midnight = (now + timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0)
    return (midnight - now).total_seconds()


def retry_after_seconds(exc: BaseException) -> float | None:
    """The provider's Retry-After (seconds form) from the exception's response headers, if any."""
    for holder in (getattr(exc, "response", None), exc):
        headers = getattr(holder, "headers", None) or getattr(holder, "litellm_response_headers", None)
        if not headers:
            continue
        for name in ("retry-after", "Retry-After"):
            raw = headers.get(name)
            if raw is not None:
                try:
                    return max(0.0, float(raw))
                except (TypeError, ValueError):
                    return None
    return None
