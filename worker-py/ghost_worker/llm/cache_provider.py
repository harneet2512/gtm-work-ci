"""GHOST_LLM_MODE=cache: every model call is made once, stored, and never repeated.

Replay-first: a request whose cassette exists is answered from disk with no network call. Record-on-miss: otherwise the
provider is called once (one call per key even under concurrency, and only one real call at a time) and the answer is written atomically.
The demo therefore replays stored decisions at no cost, and a human who leaves the recorded path costs one call, once.

Key stability. A run mints fresh UUIDs (run, candidate, set and episode ids), so a key over the raw prompt would differ on
every rehearsal. The key is computed over the request with every UUID replaced by its order of first appearance
(`<<id-0>>`, `<<id-1>>`, ...); the stored answer is canonicalised the same way and rewritten to the CURRENT request's ids
on replay, so an answer that quotes a candidate id still points at this run's candidate. Nothing else is normalised: a
global counter, a timestamp or any other value that varies between runs changes the key and is a miss (strict mode,
`GHOST_LLM_CACHE_STRICT=1`, turns a miss into an error so a determinism check can find it).

Spend guard: before a real call the OpenRouter key's usage (`GET /api/v1/key`, `data.usage`) must leave room under the
cap (default $7.50 of the user's $8 hard limit); an unreadable usage fails closed. Real calls are made one at a time (one
global recording lock), and the guard counts locally what each one spent (the reported cost, or a reserve when none is
reported, plus a reserve for every retry or fallback attempt), because the key's usage figure lags: the effective spend is
the larger of the reading and the first reading plus the local count.
"""
from __future__ import annotations

import hashlib
import json
import logging
import os
import re
import tempfile
import threading
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable, TypeVar

from ..errors import CassetteNotFoundError, InvalidModelOutputError, SpendCapError
from .provider import LLMProvider, LLMResult, TurnResult
from .usage import peek_retries

log = logging.getLogger(__name__)

R = TypeVar("R", LLMResult, TurnResult)

_UUID = re.compile(r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}")
# The delimiter nonce of the strategy, draft and judge prompts is a hash seeded by the run id (a fresh uuid) and by the
# text (which carries uuids), so it changes every run. It is masked in the key only; the prompt sent to the model is
# untouched. The extractor's TEXT nonce is seeded by the activity id, is stable, and keeps its recorded keys.
_NONCE = re.compile(r"\b(STATE|CONTEXT)-[0-9a-f]{16}\b")
_PLACEHOLDER = re.compile(r"<<id-(\d+)>>")
KEY_NAMESPACE = "ghost-cache-v1"
STATS_FILE = "cache-stats.json"
DEFAULT_CAP_USD = 7.5
DEFAULT_RESERVE_USD = 0.05
KEY_URL = "https://openrouter.ai/api/v1/key"


def canonicalize(text: str, ids: list[str]) -> tuple[str, list[str]]:
    """Replace every UUID of text with `<<id-k>>` (k = order of first appearance across calls sharing `ids`)."""
    known = list(ids)

    def swap(match: re.Match[str]) -> str:
        value = match.group(0).lower()
        if value not in known:
            known.append(value)
        return f"<<id-{known.index(value)}>>"

    return _UUID.sub(swap, _NONCE.sub(r"\1-<<nonce>>", text)), known


def _restore(text: str, ids: list[str]) -> str:
    return _PLACEHOLDER.sub(lambda m: ids[int(m.group(1))] if int(m.group(1)) < len(ids) else m.group(0), text)


def _json(obj: object) -> str:
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def _canonical_response(result: Any, ids: list[str]) -> Any:
    text = _json(result.model_dump(mode="json"))
    for index, value in enumerate(ids):
        text = re.sub(re.escape(value), f"<<id-{index}>>", text, flags=re.IGNORECASE)
    return json.loads(text)


def _reported_cost(usage: Any) -> float | None:
    cost = usage.get("cost") if hasattr(usage, "get") else None
    return float(cost) if isinstance(cost, (int, float)) and not isinstance(cost, bool) and cost >= 0 else None


def openrouter_usage(api_key: str, *, opener: Callable[..., Any] = urllib.request.urlopen, timeout: float = 10.0) -> float:
    """Total spend of the key so far (`data.usage`, dollars). Raises on any failure or malformed answer."""
    request = urllib.request.Request(KEY_URL, headers={"Authorization": f"Bearer {api_key}"})
    with opener(request, timeout=timeout) as response:
        document = json.loads(response.read().decode("utf-8"))
    usage = document["data"].get("usage") if isinstance(document, dict) and isinstance(document.get("data"), dict) else None
    if not isinstance(usage, (int, float)) or isinstance(usage, bool):
        raise ValueError("the key endpoint did not return data.usage")
    return float(usage)


class SpendGuard:
    """Refuses a real call when the key's spend plus a reserve would pass the cap. Fails closed.

    The key's usage figure lags behind the calls just made, so the guard also counts locally what this process spent
    (`record_spend`) and judges by the larger of the reading and `first reading + local count`.
    """

    def __init__(self, api_key: str | None = None, *, cap_usd: float = DEFAULT_CAP_USD, reserve_usd: float = DEFAULT_RESERVE_USD,
                 fetch: Callable[[], float] | None = None) -> None:
        self.cap_usd = cap_usd
        self.reserve_usd = reserve_usd
        self._fetch = fetch or (lambda: openrouter_usage(api_key or ""))
        self._lock = threading.Lock()
        self._baseline: float | None = None
        self._local = 0.0

    @property
    def local_spend_usd(self) -> float:
        """What this process has spent on real calls since the guard's first reading."""
        with self._lock:
            return self._local

    def check(self) -> float:
        try:
            usage = self._fetch()
        except Exception as exc:  # any failure to read the spend means we cannot prove there is room
            raise SpendCapError(f"cannot read the key's usage ({type(exc).__name__}); refusing to spend") from None
        with self._lock:
            if self._baseline is None:
                self._baseline = usage
            effective = max(usage, self._baseline + self._local)
        if effective + self.reserve_usd > self.cap_usd:
            raise SpendCapError(f"spend ${effective:.2f} plus a ${self.reserve_usd:.2f} reserve would pass the ${self.cap_usd:.2f} cap")
        return effective

    def record_spend(self, *, cost: float | None, attempts: int) -> None:
        """Count one real call: its reported cost (the reserve when none was reported) and a reserve per extra attempt.

        `cost` is None for a call that raised: every attempt it made counts a reserve."""
        extra = max(0, attempts - 1)
        spent = (self.reserve_usd * attempts) if cost is None else (cost + self.reserve_usd * extra)
        with self._lock:
            self._local += spent


class CacheStats:
    """What the web header shows: counters and whether a real call is in flight. A small JSON file, rewritten atomically."""

    def __init__(self, directory: Path) -> None:
        self.path = Path(directory) / STATS_FILE
        self._lock = threading.Lock()
        self._doc: dict[str, Any] = {"mode": "cache", "hits": 0, "recorded": 0, "misses": 0, "recording": False, "last_source": "", "last_at": "",
                                     "spend_blocked": False}
        try:
            self._doc.update({k: v for k, v in json.loads(self.path.read_text(encoding="utf-8")).items() if k in self._doc})
            self._doc["recording"] = False
        except (OSError, ValueError):
            pass

    def _write(self) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=self.path.parent, prefix=".stats.", suffix=".tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
                json.dump(self._doc, handle)
            os.replace(tmp, self.path)
        except BaseException:
            Path(tmp).unlink(missing_ok=True)
            raise

    def update(self, **changes: Any) -> None:
        with self._lock:
            self._doc.update(changes)
            self._doc["last_at"] = datetime.now(timezone.utc).isoformat()
            self._write()

    def bump(self, field: str, **changes: Any) -> None:
        with self._lock:
            self._doc[field] += 1
            self._doc.update(changes)
            self._doc["last_at"] = datetime.now(timezone.utc).isoformat()
            self._write()


class CachingProvider:
    """Replay-first, record-on-miss wrapper over a real provider (None = replay only)."""

    def __init__(self, upstream: LLMProvider | None, directory: Path, family: str, *, guard: SpendGuard | None = None,
                 strict: bool = False) -> None:
        self.upstream = upstream
        self.directory = Path(directory)
        self.family = family
        self.guard = guard
        self.strict = strict
        self._stats = CacheStats(self.directory)
        self._record_lock = threading.Lock()  # one real call at a time, across every key

    # -- public provider interface -------------------------------------------------------------------------------
    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        sys_c, ids = canonicalize(system, [])
        user_c, ids = canonicalize(user, ids)
        request = {"model_family": self.family, "system": sys_c, "user": user_c, "schema_name": schema_name, "schema": schema}
        return self._get(request, ids, LLMResult, lambda: self._need_upstream().complete_json(
            system=system, user=user, schema=schema, schema_name=schema_name))

    def complete_turn(self, *, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema: dict[str, Any], schema_name: str) -> TurnResult:
        sys_c, ids = canonicalize(system, [])
        msg_c, ids = canonicalize(_json(messages), ids)
        request = {"model_family": self.family, "system": sys_c, "messages": msg_c, "tools": tools, "schema_name": schema_name,
                   "schema": schema}
        return self._get(request, ids, TurnResult, lambda: self._need_upstream().complete_turn(
            system=system, messages=messages, tools=tools, schema=schema, schema_name=schema_name))

    # -- internals -----------------------------------------------------------------------------------------------
    def _need_upstream(self) -> LLMProvider:
        if self.upstream is None:
            raise CassetteNotFoundError("no cassette for this request and no API key: cache mode cannot record")
        return self.upstream

    @staticmethod
    def _key(request: dict[str, Any]) -> str:
        return hashlib.sha256(_json({"ns": KEY_NAMESPACE, **request}).encode("utf-8")).hexdigest()

    def _path(self, key: str) -> Path:
        return self.directory / f"{key}.json"

    def _load(self, key: str, ids: list[str], result_type: type[R]) -> R | None:
        try:
            document = json.loads(self._path(key).read_text(encoding="utf-8"))
            text = _restore(_json(document["response"]), ids)
            return result_type.model_validate(json.loads(text))
        except FileNotFoundError:
            return None
        except (OSError, ValueError, KeyError, TypeError):
            log.warning("cache cassette unreadable; it will be recorded again", extra={"key": key})
            return None

    def _get(self, request: dict[str, Any], ids: list[str], result_type: type[R], call: Callable[[], R]) -> R:
        key = self._key(request)
        hit = self._load(key, ids, result_type)
        if hit is not None:
            self._stats.bump("hits", last_source="replay")
            return hit
        if self.strict:
            self._stats.bump("misses", last_source="miss")  # a gate that swallows the error is still counted
            raise CassetteNotFoundError(f"no cassette {key} (schema={request['schema_name']}); strict cache mode never calls the network")
        with self._record_lock:
            hit = self._load(key, ids, result_type)  # another thread may have recorded it while we waited
            if hit is not None:
                self._stats.bump("hits", last_source="replay")
                return hit
            return self._record(key, request, ids, call)

    def _record(self, key: str, request: dict[str, Any], ids: list[str], call: Callable[[], R]) -> R:
        if self.guard is not None:
            try:
                self.guard.check()
            except SpendCapError:
                self._stats.update(spend_blocked=True, recording=False)
                raise
        self._stats.update(recording=True)
        retries_before = peek_retries()
        try:
            result = call()
        except BaseException:
            self._count_spend(None, peek_retries() - retries_before)
            self._stats.update(recording=False)
            raise
        self._count_spend(_reported_cost(result.usage), peek_retries() - retries_before)
        document = {"key": key, "request": request, "response": _canonical_response(result, ids)}
        self._write(key, document)
        self._stats.bump("recorded", recording=False, last_source="record")
        return result

    def _count_spend(self, cost: float | None, retries: int) -> None:
        if self.guard is not None:
            self.guard.record_spend(cost=cost, attempts=1 + max(0, retries))

    def _write(self, key: str, document: dict[str, Any]) -> None:
        self.directory.mkdir(parents=True, exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=self.directory, prefix=f".{key}.", suffix=".tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
                json.dump(document, handle, indent=2, ensure_ascii=False)
                handle.write("\n")
            os.replace(tmp, self._path(key))
        except BaseException:
            Path(tmp).unlink(missing_ok=True)
            raise
