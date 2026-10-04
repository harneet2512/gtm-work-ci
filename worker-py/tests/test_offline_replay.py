"""Replay mode must work with every non-loopback socket connection forbidden, and must not import litellm."""
from __future__ import annotations

import socket
import subprocess
import sys
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from ghost_worker.app import create_app
from ghost_worker.settings import Settings

ROOT = Path(__file__).resolve().parents[1]
LOOPBACK = {"127.0.0.1", "::1", "localhost"}


def _block_network(monkeypatch: pytest.MonkeyPatch) -> list[object]:
    attempts: list[object] = []
    real_connect = socket.socket.connect
    real_create = socket.create_connection

    def guarded_connect(self: socket.socket, address: object) -> None:
        # loopback is allowed because the Windows event loop builds its self-pipe with a loopback socketpair
        if isinstance(address, tuple) and address and address[0] in LOOPBACK:
            return real_connect(self, address)  # type: ignore[arg-type]
        attempts.append(address)
        raise AssertionError(f"network access attempted: {address!r}")

    def guarded_create(address: tuple, *args: object, **kwargs: object) -> socket.socket:
        if address[0] in LOOPBACK:
            return real_create(address, *args, **kwargs)  # type: ignore[arg-type]
        attempts.append(address)
        raise AssertionError(f"network access attempted: {address!r}")

    monkeypatch.setattr(socket.socket, "connect", guarded_connect)
    monkeypatch.setattr(socket, "create_connection", guarded_create)
    return attempts


def test_replay_serves_extract_with_network_blocked(monkeypatch: pytest.MonkeyPatch, email_request: dict) -> None:
    attempts = _block_network(monkeypatch)
    settings = Settings(_env_file=None, ghost_llm_mode="replay")
    client = TestClient(create_app(settings=settings))
    response = client.post("/v1/extract", json=email_request)
    assert response.status_code == 200, response.text
    assert response.json()["claims"]
    assert attempts == []


def test_replay_in_fresh_interpreter_never_imports_litellm_or_connects() -> None:
    code = (
        "import json, socket, sys\n"
        "from pathlib import Path\n"
        "def deny(*a, **k):\n"
        "    host = a[1][0] if len(a) > 1 and isinstance(a[1], tuple) else (a[0][0] if a and isinstance(a[0], tuple) else '')\n"
        "    if host in ('127.0.0.1', '::1', 'localhost'):\n"
        "        return real(*a, **k)\n"
        "    raise AssertionError('network: %r' % (a,))\n"
        "real = socket.socket.connect\n"
        "socket.socket.connect = deny\n"
        "real_cc = socket.create_connection\n"
        "socket.create_connection = lambda addr, *a, **k: (real_cc(addr, *a, **k) if addr[0] in ('127.0.0.1','::1','localhost') else deny(None, addr))\n"
        "from fastapi.testclient import TestClient\n"
        "from ghost_worker.app import create_app\n"
        "from ghost_worker.settings import Settings\n"
        "req = json.loads(Path('tests/fixtures/extract_email_request.json').read_text(encoding='utf-8'))\n"
        "r = TestClient(create_app(settings=Settings(_env_file=None, ghost_llm_mode='replay'))).post('/v1/extract', json=req)\n"
        "assert r.status_code == 200, r.text\n"
        "assert 'litellm' not in sys.modules, 'litellm was imported in replay mode'\n"
    )
    done = subprocess.run([sys.executable, "-c", code], cwd=ROOT, capture_output=True, text=True, timeout=180)
    assert done.returncode == 0, done.stderr[-2000:]
