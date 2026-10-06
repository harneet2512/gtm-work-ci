"""WorkerUsage (contracts/schemas/worker_usage.v1.json): what one worker request cost, returned as `usage` on the
responses of /v1/draft, /v1/strategies, /v1/judge, /v1/revise and /v1/human-delta (HAR-145 operational metrics).

A METRIC (HAR-97 M1-M5), never an eval: there is no verdict or score here. Every number is an exact sum over the
provider completions of the request; what a provider did not report is None (cached/reasoning tokens, cost) and never
estimated. A request answered from recorded cassettes (replay mode) spent nothing: usage_source is "replay" and every
count is 0 or None, whatever the cassette recorded.
"""
from __future__ import annotations

from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field

NonNegInt = Annotated[int, Field(ge=0)]


class UsageSummary(BaseModel):
    # Defined here, not on draft._Frozen: the response models in draft.py import this module.
    model_config = ConfigDict(frozen=True, extra="forbid")

    model_calls: NonNegInt
    input_tokens: NonNegInt
    output_tokens: NonNegInt
    cached_input_tokens: NonNegInt | None
    reasoning_tokens: NonNegInt | None
    tool_calls: NonNegInt
    retries: NonNegInt
    model_ms: NonNegInt
    cost_usd: Annotated[float, Field(ge=0)] | None
    models: tuple[str, ...]
    usage_source: Literal["live", "replay"]
