"""Pure ASGI request-body guard: counts the bytes actually received, so chunked bodies are bounded too."""
from __future__ import annotations

from collections.abc import Awaitable, Callable, MutableMapping
from typing import Any

from fastapi import HTTPException
from fastapi.responses import JSONResponse

from .models import ErrorEnvelope

Scope = MutableMapping[str, Any]
Message = MutableMapping[str, Any]
Receive = Callable[[], Awaitable[Message]]
Send = Callable[[Message], Awaitable[None]]
ASGIApp = Callable[[Scope, Receive, Send], Awaitable[None]]


class BodyTooLarge(HTTPException):
    """Raised from `receive` once the received body exceeds the limit (an HTTPException subclass so FastAPI's
    body parsing re-raises it unchanged instead of turning it into a generic 400)."""

    def __init__(self, max_bytes: int) -> None:
        super().__init__(status_code=422, detail=too_large_message(max_bytes))


def too_large_message(max_bytes: int) -> str:
    return f"request body exceeds {max_bytes} bytes"


def too_large_response(max_bytes: int) -> JSONResponse:
    envelope = ErrorEnvelope.of("request_too_large", too_large_message(max_bytes))
    return JSONResponse(status_code=422, content=envelope.model_dump())


class BodySizeLimitMiddleware:
    def __init__(self, app: ASGIApp, max_bytes: int) -> None:
        self.app = app
        self.max_bytes = max_bytes

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        declared = dict(scope.get("headers", [])).get(b"content-length", b"")
        if declared.isdigit() and int(declared) > self.max_bytes:
            await too_large_response(self.max_bytes)(scope, receive, send)
            return

        received = 0

        async def counting_receive() -> Message:
            nonlocal received
            message = await receive()
            if message["type"] == "http.request":
                received += len(message.get("body", b""))
                if received > self.max_bytes:
                    raise BodyTooLarge(self.max_bytes)
            return message

        await self.app(scope, counting_receive, send)
