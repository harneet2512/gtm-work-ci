"""Core context client: run-token bearer, configured core URL only, byte cap, every failure a CoreContextError."""
from __future__ import annotations

import json

import httpx
import pytest

from ghost_worker.draft.core_client import CoreContextClient, ToolParams, parse_tool_name, tool_params
from ghost_worker.errors import CoreContextError

BASE = "http://core.internal:8080"
TOKEN = "run-token-" + "x" * 30


def packet(tool: str = "people", **overrides: object) -> dict:
    return {"access_id": 7, "tool": tool, "items": [{"person_id": "p"}], "truncated": False, "bytes": 20, **overrides}


def make(handler, *, max_bytes: int = 4096) -> tuple[CoreContextClient, list[httpx.Request]]:
    seen: list[httpx.Request] = []

    def recording(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return handler(request)

    client = CoreContextClient(BASE, TOKEN, timeout_s=2.0, max_bytes=max_bytes,
                               transport=httpx.MockTransport(recording))
    return client, seen


def test_pull_sends_bearer_to_the_configured_core_and_parses_the_packet() -> None:
    client, seen = make(lambda r: httpx.Response(200, json=packet()))
    result = client.pull("people", ToolParams(field_path="blockers", limit=5), timeout_s=1.0)
    assert result.access_id == 7 and result.tool == "people"
    (request,) = seen
    assert str(request.url).startswith(f"{BASE}/internal/ctx/people")
    assert request.url.params["limit"] == "5" and request.url.params["field_path"] == "blockers"
    assert request.headers["authorization"] == f"Bearer {TOKEN}"
    assert request.method == "GET"
    assert set(request.url.params) == {"limit", "field_path"}  # no account parameter, ever


@pytest.mark.parametrize("status", [401, 403, 404, 500])
def test_non_200_is_a_core_error_without_leaking_the_token_or_body(status: int) -> None:
    client, _ = make(lambda r: httpx.Response(status, text=f"secret detail {TOKEN}"))
    with pytest.raises(CoreContextError) as caught:
        client.pull("state", ToolParams(), timeout_s=1.0)
    assert str(status) in str(caught.value)
    assert TOKEN not in str(caught.value) and "secret detail" not in str(caught.value)


def test_timeout_and_transport_errors_are_core_errors() -> None:
    def timeout(request: httpx.Request) -> httpx.Response:
        raise httpx.ReadTimeout("slow", request=request)

    def refused(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("refused", request=request)

    for handler in (timeout, refused):
        client, _ = make(handler)
        with pytest.raises(CoreContextError):
            client.pull("state", ToolParams(), timeout_s=1.0)


def test_oversized_response_is_rejected() -> None:
    big = packet(items=[{"blob": "x" * 5000}])
    client, _ = make(lambda r: httpx.Response(200, json=big), max_bytes=1024)
    with pytest.raises(CoreContextError, match="byte cap"):
        client.pull("people", ToolParams(), timeout_s=1.0)


@pytest.mark.parametrize("body", [b"not json", b"[1]", json.dumps({"access_id": 1}).encode(),
                                  json.dumps(packet(tool="state")).encode()])
def test_malformed_or_mismatched_packet_is_rejected(body: bytes) -> None:
    client, _ = make(lambda r: httpx.Response(200, content=body))
    with pytest.raises(CoreContextError):
        client.pull("people", ToolParams(), timeout_s=1.0)


def test_redirects_are_not_followed() -> None:
    client, seen = make(lambda r: httpx.Response(302, headers={"location": "http://evil.example/x"}))
    with pytest.raises(CoreContextError):
        client.pull("people", ToolParams(), timeout_s=1.0)
    assert len(seen) == 1


def test_context_manager_closes_the_client() -> None:
    with CoreContextClient(BASE, TOKEN, timeout_s=1, max_bytes=100,
                           transport=httpx.MockTransport(lambda r: httpx.Response(200, json=packet()))) as client:
        client.pull("people", ToolParams(), timeout_s=1)
    with pytest.raises(RuntimeError):
        client._http.get(BASE)


def test_default_transport_can_be_built_without_network() -> None:
    client = CoreContextClient(BASE, TOKEN, timeout_s=1, max_bytes=100)
    client.close()


# --- tool argument validation ------------------------------------------------------------------

def test_tool_params_accepts_known_arguments() -> None:
    assert tool_params({"field_path": "blockers", "limit": 3}) == ToolParams(field_path="blockers", limit=3)
    assert tool_params({}) == ToolParams()
    assert ToolParams(limit=3).as_query() == {"limit": 3} and ToolParams().as_query() == {}


@pytest.mark.parametrize("arguments", [
    {"account_id": "0a0c0000-0000-4000-8000-000000000002"},  # no cross-account parameter
    {"limit": 0}, {"limit": 21}, {"limit": "5"}, {"limit": True},
    {"field_path": "not_a_field"}, {"field_path": 3},
])
def test_tool_params_rejects_bad_arguments(arguments: dict) -> None:
    with pytest.raises(ValueError):
        tool_params(arguments)


def test_tool_names_are_a_closed_set() -> None:
    assert parse_tool_name("commitments") == "commitments"
    with pytest.raises(ValueError, match="unknown tool"):
        parse_tool_name("shell")


def test_pull_parses_the_world_cutoff_core_served_and_sends_no_way_to_set_it() -> None:
    cutoff = "2026-09-02T10:00:00.000001Z"
    client, seen = make(lambda r: httpx.Response(200, json=packet("state", world_as_of=cutoff)))
    result = client.pull("state", ToolParams(limit=3), timeout_s=1.0)
    assert result.world_as_of == cutoff
    assert "world_as_of" not in result.as_payload()  # a model never sees or sets the cutoff
    assert set(seen[0].url.params) == {"limit"}


def test_pull_without_a_cutoff_reports_none_for_both_null_and_absent() -> None:
    for body in (packet(), packet(world_as_of=None)):
        client, _ = make(lambda r, b=body: httpx.Response(200, json=b))
        assert client.pull("people", ToolParams(), timeout_s=1.0).world_as_of is None


def test_tool_params_rejects_a_world_cutoff_argument() -> None:
    with pytest.raises(ValueError, match="unsupported arguments: world_as_of"):
        tool_params({"world_as_of": "2020-01-01T00:00:00Z"})
