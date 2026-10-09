"""Run-token client for core GET /internal/ctx/{tool}. The URL comes from settings only, never a request."""
from __future__ import annotations

import json
import time
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import Annotated, Any, Literal, cast, get_args

import httpx
from pydantic import BaseModel, ConfigDict, Field, ValidationError

from ..errors import CoreContextError
from ..models import FieldPath
from ..models.frozen import FrozenMap

ToolName = Literal["state", "recent_diffs", "evidence", "activities", "people", "commitments"]
TOOL_NAMES: tuple[str, ...] = get_args(ToolName)
MIN_LIMIT, MAX_LIMIT = 1, 20
_FIELD_PATHS = frozenset(get_args(FieldPath))
_ALLOWED_ARGUMENTS = frozenset({"field_path", "limit"})  # deliberately no account parameter


class ContextPacket(BaseModel):
    """contracts/schemas/context_packet.v1.json"""

    model_config = ConfigDict(frozen=True, extra="forbid")

    access_id: int
    tool: ToolName
    items: tuple[FrozenMap, ...]
    truncated: bool
    bytes: Annotated[int, Field(ge=0)]
    # World-time cutoff core served the pull at (ADR-0019), derived from the run; null only from a core that predates ADR-0019.
    world_as_of: str | None = None
    limits: FrozenMap | None = None

    def as_payload(self) -> dict[str, Any]:
        """The one serialisation of a packet shown to a model (agent tool result and skill context)."""
        return {"access_id": self.access_id, "tool": self.tool, "truncated": self.truncated,
                "items": [dict(item) for item in self.items]}


def dump_json(payload: Mapping[str, Any]) -> str:
    """Stable JSON text for anything shown to a model (so replay keys are deterministic)."""
    return json.dumps(payload, sort_keys=True, ensure_ascii=False)


def parse_tool_name(name: str) -> ToolName:
    if name not in TOOL_NAMES:
        raise ValueError(f"unknown tool {name!r}")
    return cast(ToolName, name)


@dataclass(frozen=True)
class ToolParams:
    field_path: FieldPath | None = None
    limit: int | None = None

    def as_query(self) -> dict[str, Any]:
        return {k: v for k, v in (("field_path", self.field_path), ("limit", self.limit)) if v is not None}


def tool_params(arguments: Mapping[str, Any]) -> ToolParams:
    """Validate model-supplied tool arguments; raises ValueError with a message safe to show the model."""
    unknown = set(arguments) - _ALLOWED_ARGUMENTS
    if unknown:
        raise ValueError(f"unsupported arguments: {', '.join(sorted(unknown))}")
    field_path = arguments.get("field_path")
    if "field_path" in arguments and field_path not in _FIELD_PATHS:
        raise ValueError("field_path must be one of the account-state field paths")
    limit = arguments.get("limit")
    if "limit" in arguments and (isinstance(limit, bool) or not isinstance(limit, int)
                                 or not MIN_LIMIT <= limit <= MAX_LIMIT):
        raise ValueError(f"limit must be an integer between {MIN_LIMIT} and {MAX_LIMIT}")
    return ToolParams(field_path=cast("FieldPath | None", field_path), limit=limit)


MAX_RETRY_AFTER_S = 5.0


def _is_projection_pending(body: bytes) -> bool:
    try:
        return json.loads(body).get("error", {}).get("code") == "projection_pending"
    except (ValueError, AttributeError):
        return False


def _retry_after(header: str | None) -> float:
    """Seconds from a Retry-After header (delta form); 1 s when absent or unreadable, never above 5 s."""
    try:
        return min(max(float(header), 0.05), MAX_RETRY_AFTER_S) if header else 1.0
    except ValueError:
        return 1.0


class CoreContextClient:
    def __init__(self, base_url: str, run_token: str, *, timeout_s: float, max_bytes: int,
                 transport: httpx.BaseTransport | None = None,
                 clock: Callable[[], float] = time.monotonic, sleep: Callable[[float], None] = time.sleep) -> None:
        self._clock = clock
        self._sleep = sleep
        self._max_bytes = max_bytes
        self._timeout_s = timeout_s
        self._http = httpx.Client(base_url=base_url, transport=transport, follow_redirects=False,
                                  headers={"Authorization": f"Bearer {run_token}", "Accept": "application/json"})

    def __enter__(self) -> CoreContextClient:
        return self

    def __exit__(self, *_: object) -> None:
        self.close()

    def close(self) -> None:
        self._http.close()

    def pull(self, tool: ToolName, params: ToolParams, *, timeout_s: float) -> ContextPacket:
        """One context pull. While core's graph projection of the account is unfinished it answers 503
        `projection_pending` with Retry-After; the pull waits and retries within `timeout_s` (the barrier
        keeps an agent from reading a world the graph has not caught up with)."""
        budget = min(self._timeout_s, timeout_s)
        deadline = self._clock() + budget
        while True:
            remaining = deadline - self._clock()
            body, retry_after = self._once(tool, params, max(remaining, 0.001))
            if body is not None:
                return self._parse(body, tool)
            if retry_after is None or self._clock() + retry_after >= deadline:
                raise CoreContextError(f"core projection of the account is not complete for {tool}")
            self._sleep(retry_after)

    def _once(self, tool: ToolName, params: ToolParams, timeout: float) -> tuple[bytes | None, float | None]:
        """(body, None) on 200; (None, delay) on a projection_pending 503; raises otherwise."""
        try:
            with self._http.stream("GET", f"/internal/ctx/{tool}", params=params.as_query(),
                                   timeout=timeout) as response:
                if response.status_code == 503:
                    body = self._read_capped(response, tool)
                    if _is_projection_pending(body):
                        return None, _retry_after(response.headers.get("Retry-After"))
                    raise CoreContextError(f"core returned HTTP 503 for {tool}")
                if response.status_code != 200:
                    raise CoreContextError(f"core returned HTTP {response.status_code} for {tool}", rejected=response.status_code == 400)
                return self._read_capped(response, tool), None
        except httpx.HTTPError as exc:
            raise CoreContextError(f"core request for {tool} failed: {type(exc).__name__}") from None

    def _read_capped(self, response: httpx.Response, tool: str) -> bytes:
        received = bytearray()
        for chunk in response.iter_bytes():
            received.extend(chunk)
            if len(received) > self._max_bytes:
                raise CoreContextError(f"core response for {tool} exceeds the {self._max_bytes} byte cap")
        return bytes(received)

    @staticmethod
    def _parse(body: bytes, tool: str) -> ContextPacket:
        try:
            packet = ContextPacket.model_validate_json(body)
        except ValidationError:
            raise CoreContextError(f"core returned a malformed context packet for {tool}") from None
        if packet.tool != tool:
            raise CoreContextError(f"core answered tool {packet.tool!r} to a request for {tool!r}")
        return packet
