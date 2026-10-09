"""Explicit error hierarchy. Messages may carry detail; the API layer never forwards them to clients."""
from __future__ import annotations


class WorkerError(Exception):
    """Base class for every expected worker failure.

    `code` and `public_message` are what the API envelope shows (HTTP 502); `str(exc)` is for logs only."""

    code = "provider_error"
    public_message = "model provider failed to produce a usable result"


class ConfigError(WorkerError):
    """Invalid or missing configuration (e.g. live mode without an API key)."""


class LLMError(WorkerError):
    """A model call failed."""


class ProviderError(LLMError):
    """The upstream provider failed. `retryable` means the fallback model may be tried."""

    def __init__(self, message: str, *, retryable: bool = False) -> None:
        super().__init__(message)
        self.retryable = retryable


class InvalidModelOutputError(LLMError):
    """The model did not return usable JSON (after the permitted retry)."""


class SpendCapError(LLMError):
    """Cache mode refused a real call: the key's spend would pass the cap, or it could not be read (fails closed)."""

    code = "spend_cap"
    public_message = "the model spend cap would be exceeded; no call was made"


class CassetteNotFoundError(LLMError):
    """Replay mode asked for a recording that does not exist; the network is never used instead.

    A structured code of its own (HTTP 502, as every LLMError), so a client can tell "no recording" from a provider failure."""

    code = "cassette_not_found"
    public_message = "no recorded answer exists for this request; replay never calls a model"


class UnsupportedExtractorVersionError(WorkerError):
    """The request names an extractor version this worker does not implement (client error)."""


class InvalidRequestError(WorkerError):
    """The request is well-formed but inconsistent (client error, 422). The message is safe to return."""


class CoreContextError(WorkerError):
    """A core /internal/ctx pull failed (403, timeout, oversize, malformed packet). Never carries the run token.

    `rejected` is True for an HTTP 400: core refused the model's arguments (for example an evidence pull without field_path). That is
    the model's mistake to correct, not an outage, so the agent loop reports it to the model instead of failing the run."""

    def __init__(self, message: str = "", *, rejected: bool = False) -> None:
        super().__init__(message)
        self.rejected = rejected

    code = "core_unavailable"
    public_message = "core context pull failed"


class UngroundedProposalError(WorkerError):
    """The proposal cites evidence or people that no logged context pull in this run returned."""

    code = "ungrounded_proposal"
    public_message = "the proposal could not be grounded in the context pulled for this run"


class ToolBudgetExceededError(WorkerError):
    """The agent kept asking for tools after its call budget was spent."""

    code = "tool_budget_exceeded"
    public_message = "the agent exceeded its context-pull budget"


class InconsistentDraftError(WorkerError):
    """The drafting skill's output contradicts the account decision (who to involve / not involve).

    A decision/skill consistency failure, not a provider failure: deliberately not an LLMError, so it is
    never retried as unusable JSON and never reported as `provider_error`."""

    code = "inconsistent_draft"
    public_message = "the drafted action did not follow the account decision"


class DraftDeadlineError(WorkerError):
    """The overall /v1/draft deadline elapsed."""

    code = "deadline_exceeded"
    public_message = "the draft deadline was exceeded"


class ProviderUnavailableError(ProviderError):
    """The provider refused the call in a way no retry can fix (401/402/403, no credits, quota, bad key), or
    the worker's own circuit breaker is open. Core must not retry it (HTTP 424, HAR-135)."""

    code = "provider_unavailable_nonretryable"
    public_message = "model provider is unavailable and retrying cannot help (credits, quota or authentication)"

    def __init__(self, message: str) -> None:
        super().__init__(message, retryable=False)


class RateLimitedError(ProviderError):
    """HTTP 429 (or a rate-limit exception): retryable, after `retry_after_s` when the provider said so."""

    def __init__(self, message: str, *, retry_after_s: float | None = None) -> None:
        super().__init__(message, retryable=True)
        self.retry_after_s = retry_after_s


class DailyCapError(ProviderUnavailableError):
    """The free-models-per-day cap is spent: no retry helps until it resets, so the breaker stays open for
    `resume_in_s` (until 00:00 UTC) with this reason."""

    def __init__(self, message: str, *, resume_in_s: float) -> None:
        super().__init__(message)
        self.resume_in_s = resume_in_s


class InvalidStrategiesError(WorkerError):
    """The generated strategy set is not usable: not three materially distinct, complete candidates. Core
    retries invalid output once, then fails the run."""

    code = "invalid_strategies"
    public_message = "the generated strategies were not three materially distinct, complete candidates"
