"""GHOST_LLM_MODE=cache: replay-first, record-on-miss, once. A model call is made once, stored, never repeated."""
from __future__ import annotations

import json
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Any

import pytest

from ghost_worker.errors import CassetteNotFoundError, SpendCapError
from ghost_worker.llm.cache_provider import CachingProvider, SpendGuard, canonicalize, openrouter_usage
from ghost_worker.llm.factory import build_provider
from ghost_worker.llm.provider import LLMResult, ToolCall, TurnResult
from ghost_worker.settings import Settings

SCHEMA = {"type": "object", "properties": {"a": {"type": "string"}}}
ID_A = "0d3a0000-0000-4000-8000-00000000000a"
ID_B = "0d3a0000-0000-4000-8000-00000000000b"
ID_C = "7e5c1111-2222-4333-8444-555566667777"
ID_D = "9a9a9a9a-bbbb-4ccc-8ddd-eeeeeeeeeeee"


class Upstream:
    """Counts calls; answers with content that quotes the first uuid in the prompt (so id mapping is observable)."""

    def __init__(self, delay: float = 0.0) -> None:
        self.calls = 0
        self.delay = delay
        self.active = self.max_concurrent = 0
        self._lock = threading.Lock()

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        with self._lock:
            self.calls += 1
            self.active += 1
            self.max_concurrent = max(self.max_concurrent, self.active)
        time.sleep(self.delay)
        with self._lock:
            self.active -= 1
        found = [t for t in user.replace('"', " ").split() if len(t) == 36 and t.count("-") == 4]
        return LLMResult(content={"echo": found[0] if found else "", "n": 1}, model="live/model-x", usage={"cost": 0.01})

    def complete_turn(self, *, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema: dict[str, Any], schema_name: str) -> TurnResult:
        with self._lock:
            self.calls += 1
        return TurnResult(tool_calls=(ToolCall(id="call-1", name="pull", arguments={"id": ID_A}),), model="live/model-x")


def provider(tmp_path: Path, upstream: Upstream | None, **kw: Any) -> CachingProvider:
    return CachingProvider(upstream, tmp_path, "model-x", **kw)


def ask(p: CachingProvider, user: str = "hello", system: str = "sys") -> LLMResult:
    return p.complete_json(system=system, user=user, schema=SCHEMA, schema_name="s")


class TestReplayFirstRecordOnMiss:
    def test_a_new_request_records_once_and_the_identical_second_request_never_hits_the_provider(self, tmp_path):
        up = Upstream()
        p = provider(tmp_path, up)
        first = ask(p)
        second = ask(p)
        assert up.calls == 1
        assert first.content == second.content and second.model == "live/model-x"
        assert len(list(tmp_path.glob("*.json"))) == 2  # the cassette and the stats file

    def test_a_different_request_is_a_new_miss(self, tmp_path):
        up = Upstream()
        p = provider(tmp_path, up)
        ask(p, "one")
        ask(p, "two")
        ask(p, "one")
        assert up.calls == 2

    def test_a_new_process_replays_what_the_last_one_recorded(self, tmp_path):
        up1 = Upstream()
        ask(provider(tmp_path, up1))
        up2 = Upstream()
        ask(provider(tmp_path, up2))
        assert (up1.calls, up2.calls) == (1, 0)

    def test_tool_turns_are_cached_too(self, tmp_path):
        up = Upstream()
        p = provider(tmp_path, up)
        args = dict(system="s", messages=[{"role": "user", "content": "x"}], tools=[{"type": "function"}], schema=SCHEMA, schema_name="s")
        a, b = p.complete_turn(**args), p.complete_turn(**args)
        assert up.calls == 1 and a == b and b.tool_calls[0].name == "pull"

    def test_a_corrupt_cassette_is_rerecorded_not_trusted(self, tmp_path):
        up = Upstream()
        p = provider(tmp_path, up)
        ask(p)
        cassette = next(c for c in tmp_path.glob("*.json") if c.name != "cache-stats.json")
        cassette.write_text("{not json", encoding="utf-8")
        ask(p)
        assert up.calls == 2
        json.loads(cassette.read_text(encoding="utf-8"))  # rewritten atomically and valid

    def test_no_temp_files_are_left_behind(self, tmp_path):
        ask(provider(tmp_path, Upstream()))
        assert list(tmp_path.glob("*.tmp")) == []


class TestConcurrency:
    def test_concurrent_identical_misses_make_one_call(self, tmp_path):
        up = Upstream(delay=0.2)
        p = provider(tmp_path, up)
        results: list[LLMResult] = []
        threads = [threading.Thread(target=lambda: results.append(ask(p))) for _ in range(8)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        assert up.calls == 1 and len(results) == 8 and len({json.dumps(r.content) for r in results}) == 1

    def test_real_calls_are_serialised_across_keys_so_the_spend_guard_sees_each_one(self, tmp_path):
        up = Upstream(delay=0.1)
        p = provider(tmp_path, up)
        threads = [threading.Thread(target=ask, args=(p, f"q{i}")) for i in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        assert up.calls == 4 and up.max_concurrent == 1

    def test_a_replayed_answer_never_waits_for_a_recording_in_flight(self, tmp_path):
        ask(provider(tmp_path, Upstream()), "stored")
        slow = Upstream(delay=0.5)
        p = provider(tmp_path, slow)
        recording = threading.Thread(target=ask, args=(p, "new"))
        recording.start()
        time.sleep(0.1)
        started = time.monotonic()
        ask(p, "stored")
        assert time.monotonic() - started < 0.3
        recording.join()


class TestKeyStability:
    def test_the_key_does_not_depend_on_which_uuids_a_run_happened_to_get(self, tmp_path):
        up = Upstream()
        p = provider(tmp_path, up)
        ask(p, f"candidate {ID_A} replied to {ID_B}; again {ID_A}")
        again = ask(p, f"candidate {ID_C} replied to {ID_D}; again {ID_C}")
        assert up.calls == 1, "a fresh run with other ids but the same shape must replay"
        assert again.content["echo"] == ID_C, "the replayed answer is rewritten to this run's ids"

    def test_a_different_shape_is_not_confused_by_the_id_mapping(self, tmp_path):
        up = Upstream()
        p = provider(tmp_path, up)
        ask(p, f"{ID_A} then {ID_B}")
        ask(p, f"{ID_A} then {ID_A}")  # same ids twice: a different structure
        assert up.calls == 2

    def test_non_id_differences_still_miss(self, tmp_path):
        up = Upstream()
        p = provider(tmp_path, up)
        ask(p, f"{ID_A} price 10")
        ask(p, f"{ID_C} price 11")
        assert up.calls == 2

    def test_canonicalize_numbers_uuids_by_first_appearance_case_insensitively(self):
        text, ids = canonicalize(f"x {ID_A.upper()} y {ID_B} z {ID_A}", [])
        assert ids == [ID_A, ID_B]
        assert text.count("<<id-0>>") == 2 and "<<id-1>>" in text

    def test_the_key_is_identical_in_another_process(self, tmp_path):
        """Stable across processes: no hash randomisation, no counters, no time in the key."""
        up = Upstream()
        ask(provider(tmp_path, up), f"for {ID_A}")
        code = (
            "import sys, pathlib;"
            "from ghost_worker.llm.cache_provider import CachingProvider;"
            "p=CachingProvider(None, pathlib.Path(sys.argv[1]), 'model-x', strict=True);"
            f"r=p.complete_json(system='sys', user='for {ID_C}', schema={SCHEMA!r}, schema_name='s');"
            "print(r.content['echo'])")
        done = subprocess.run([sys.executable, "-c", code, str(tmp_path)], capture_output=True, text=True,
                              env={"PYTHONHASHSEED": "random", "PATH": ""}, check=False)
        assert done.returncode == 0, done.stderr
        assert done.stdout.strip() == ID_C


class TestStrictAndNoUpstream:
    def test_strict_mode_never_calls_the_network_and_names_the_miss(self, tmp_path):
        up = Upstream()
        p = provider(tmp_path, up, strict=True)
        with pytest.raises(CassetteNotFoundError):
            ask(p)
        assert up.calls == 0

    def test_without_an_upstream_a_miss_is_an_error_not_a_crash(self, tmp_path):
        with pytest.raises(CassetteNotFoundError, match="no API key"):
            ask(provider(tmp_path, None))


class TestSpendGuard:
    def test_a_miss_is_refused_when_the_key_usage_would_pass_the_cap(self, tmp_path):
        up = Upstream()
        guard = SpendGuard(cap_usd=7.5, reserve_usd=0.05, fetch=lambda: 7.46)
        with pytest.raises(SpendCapError):
            ask(provider(tmp_path, up, guard=guard))
        assert up.calls == 0

    def test_a_miss_below_the_cap_goes_through_and_a_hit_never_asks(self, tmp_path):
        asked: list[int] = []
        guard = SpendGuard(cap_usd=7.5, reserve_usd=0.05, fetch=lambda: asked.append(1) or 1.0)
        up = Upstream()
        p = provider(tmp_path, up, guard=guard)
        ask(p)
        ask(p)
        assert up.calls == 1 and len(asked) == 1

    def test_an_unreadable_usage_fails_closed(self, tmp_path):
        def boom() -> float:
            raise OSError("network down")

        with pytest.raises(SpendCapError, match="cannot read"):
            ask(provider(tmp_path, Upstream(), guard=SpendGuard(fetch=boom)))

    def test_the_guard_reads_data_usage_from_the_key_endpoint(self):
        seen: dict[str, str] = {}

        class Resp:
            def __enter__(self): return self
            def __exit__(self, *a): return False
            def read(self): return b'{"data": {"usage": 3.25, "limit": null}}'

        def opener(req, timeout):
            seen["url"], seen["auth"] = req.full_url, req.get_header("Authorization")
            return Resp()

        assert openrouter_usage("sk-or-test", opener=opener) == 3.25
        assert seen == {"url": "https://openrouter.ai/api/v1/key", "auth": "Bearer sk-or-test"}

    def test_a_malformed_usage_document_is_an_error(self):
        class Resp:
            def __enter__(self): return self
            def __exit__(self, *a): return False
            def read(self): return b'{"data": {}}'

        with pytest.raises(ValueError):
            openrouter_usage("k", opener=lambda req, timeout: Resp())

    def test_the_default_cap_is_seven_fifty_under_the_eight_dollar_limit(self):
        assert SpendGuard(fetch=lambda: 0.0).cap_usd == 7.5


class TestStatsForTheHeader:
    def stats(self, tmp_path: Path) -> dict[str, Any]:
        return json.loads((tmp_path / "cache-stats.json").read_text(encoding="utf-8"))

    def test_hits_and_recordings_are_counted_and_the_last_source_is_named(self, tmp_path):
        p = provider(tmp_path, Upstream())
        ask(p)
        s = self.stats(tmp_path)
        assert (s["recorded"], s["hits"], s["last_source"], s["recording"]) == (1, 0, "record", False)
        ask(p)
        s = self.stats(tmp_path)
        assert (s["recorded"], s["hits"], s["last_source"]) == (1, 1, "replay")

    def test_recording_is_true_while_a_miss_is_in_flight(self, tmp_path):
        gate = threading.Event()
        seen: dict[str, Any] = {}

        class Slow(Upstream):
            def complete_json(self, **kw: Any) -> LLMResult:
                seen.update(json.loads((tmp_path / "cache-stats.json").read_text(encoding="utf-8")))
                gate.set()
                return super().complete_json(**kw)

        ask(provider(tmp_path, Slow()))
        assert gate.is_set() and seen["recording"] is True

    def test_counts_survive_a_restart(self, tmp_path):
        ask(provider(tmp_path, Upstream()))
        p2 = provider(tmp_path, Upstream())
        ask(p2)
        s = self.stats(tmp_path)
        assert (s["recorded"], s["hits"]) == (1, 1)

    def test_a_refused_spend_is_visible(self, tmp_path):
        with pytest.raises(SpendCapError):
            ask(provider(tmp_path, Upstream(), guard=SpendGuard(fetch=lambda: 99.0)))
        assert self.stats(tmp_path)["spend_blocked"] is True


class TestFactory:
    def test_cache_mode_builds_a_caching_provider_in_the_demo_cache_dir(self, tmp_path):
        s = Settings(_env_file=None, ghost_llm_mode="cache", openrouter_api_key="k", ghost_llm_cache_dir=tmp_path)
        p = build_provider(s)
        assert isinstance(p, CachingProvider) and p.directory == tmp_path and p.upstream is not None and p.guard is not None

    def test_cache_mode_without_a_key_still_replays(self, tmp_path):
        s = Settings(_env_file=None, ghost_llm_mode="cache", ghost_llm_cache_dir=tmp_path)
        p = build_provider(s)
        assert p.upstream is None
        with pytest.raises(CassetteNotFoundError):
            ask(p)

    def test_strict_and_cap_come_from_settings(self, tmp_path):
        s = Settings(_env_file=None, ghost_llm_mode="cache", openrouter_api_key="k", ghost_llm_cache_dir=tmp_path,
                     ghost_llm_cache_strict=True, ghost_llm_spend_cap_usd=3.0)
        p = build_provider(s)
        assert p.strict is True and p.guard.cap_usd == 3.0
