"""Ask-token client for core POST /internal/ask/tools/{tool}. The URL comes from settings only, never a request."""
from __future__ import annotations

from typing import Any

import httpx

from ..errors import CoreContextError


def _refusal(tool: str, message: str) -> dict[str, Any]:
    return {"tool": tool, "ok": False, "empty": True, "truncated": False, "data": {"error": message}, "links": []}


class AskToolClient:
    def __init__(self, base_url: str, ask_token: str, *, timeout_s: float, max_bytes: int,
                 transport: httpx.BaseTransport | None = None) -> None:
        self._timeout_s = timeout_s
        self._max_bytes = max_bytes
        self._http = httpx.Client(base_url=base_url, transport=transport, follow_redirects=False,
                                  headers={"Authorization": f"Bearer {ask_token}", "Accept": "application/json"})

    def __enter__(self) -> AskToolClient:
        return self

    def __exit__(self, *_: object) -> None:
        self._http.close()

    def call(self, tool: str, args: dict[str, Any], *, timeout_s: float) -> dict[str, Any]:
        """One tool call. A bad request (400/404) is a refusal result the model can read; anything else that is
        not a 200 is a core failure (the token was refused, core is down) and ends the question."""
        timeout = max(min(self._timeout_s, timeout_s), 0.001)
        try:
            response = self._http.post(f"/internal/ask/tools/{tool}", json={"args": args}, timeout=timeout)
        except httpx.HTTPError as exc:
            raise CoreContextError(f"core request for {tool} failed: {type(exc).__name__}") from None
        if response.status_code in (400, 404):
            return _refusal(tool, "core refused these arguments or does not have this tool")
        if response.status_code != 200:
            raise CoreContextError(f"core returned HTTP {response.status_code} for ask tool {tool}")
        if len(response.content) > self._max_bytes:
            raise CoreContextError(f"core response for {tool} exceeds the {self._max_bytes} byte cap")
        try:
            body = response.json()
        except ValueError:
            raise CoreContextError(f"core returned malformed JSON for {tool}") from None
        if not isinstance(body, dict) or not {"ok", "empty", "data", "links"} <= set(body):
            raise CoreContextError(f"core returned a malformed tool result for {tool}")
        return body
