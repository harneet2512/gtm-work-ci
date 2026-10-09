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


def test_strict_miss_is_counted_in_the_stats_file_even_when_the_caller_swallows_the_error(tmp_path: Path) -> None:
    p = provider(tmp_path, None, strict=True)
    for _ in range(2):
        with pytest.raises(CassetteNotFoundError):
            ask(p)
    stats = json.loads((tmp_path / "cache-stats.json").read_text(encoding="utf-8"))
    assert stats["misses"] == 2 and stats["hits"] == 0


def test_a_transient_permission_error_on_the_stats_or_cassette_replace_does_not_fail_the_call(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """Windows: the destination is briefly open in another process; the first real record died with a 500 internal_error on it."""
    import os

    real, failures = os.replace, {"n": 0}

    def flaky(src: str, dst: Any) -> None:
        if failures["n"] < 3:
            failures["n"] += 1
            raise PermissionError(5, "Access is denied")
        real(src, dst)

    monkeypatch.setattr("ghost_worker.llm.cache_provider.os.replace", flaky)
    monkeypatch.setattr("ghost_worker.llm.cache_provider.time.sleep", lambda _s: None)
    up = Upstream()
    p = provider(tmp_path, up)
    ask(p)
    assert up.calls == 1 and failures["n"] == 3
    assert json.loads((tmp_path / "cache-stats.json").read_text(encoding="utf-8"))["recorded"] == 1


# --- HAR-124: bounded record concurrency (a judging round is 15+ minute-long calls; one at a time outlasts every deadline) ---------------

def _run_threads(p: CachingProvider, users: list[str]) -> None:
    threads = [threading.Thread(target=ask, args=(p, u)) for u in users]
    for t in threads:
        t.start()
    for t in threads:
        t.join()


def test_record_concurrency_n_runs_up_to_n_real_calls_at_once_and_no_more(tmp_path: Path) -> None:
    up = Upstream(delay=0.15)
    p = provider(tmp_path, up, record_concurrency=3)
    _run_threads(p, [f"q{i}" for i in range(9)])
    assert up.calls == 9 and 2 <= up.max_concurrent <= 3
    stats = json.loads((tmp_path / "cache-stats.json").read_text(encoding="utf-8"))
    assert stats["recorded"] == 9 and stats["recording"] is False


def test_the_same_request_is_never_made_twice_at_once_even_with_concurrency(tmp_path: Path) -> None:
    up = Upstream(delay=0.15)
    p = provider(tmp_path, up, record_concurrency=4)
    _run_threads(p, ["same"] * 8)
    assert up.calls == 1


def test_calls_in_flight_hold_a_reserve_against_the_spend_cap(tmp_path: Path) -> None:
    # usage 7.30, cap 7.50, reserve 0.05: room for three calls in flight (7.30 + 3 * 0.05 = 7.45), the fifth is refused
    guard = SpendGuard("k", cap_usd=7.5, reserve_usd=0.05, fetch=lambda: 7.30)
    assert guard.check(0) == 7.30 and guard.check(2) == 7.30
    with pytest.raises(SpendCapError):
        guard.check(4)


def test_the_default_stays_one_real_call_at_a_time(tmp_path: Path) -> None:
    up = Upstream(delay=0.1)
    _run_threads(provider(tmp_path, up), [f"q{i}" for i in range(4)])
    assert up.max_concurrent == 1


# --- HAR-124: Event N is ingested under a fresh activity id every run; its extraction prompt must still have one key ----------------------

def test_the_extraction_prompt_of_the_same_email_under_a_new_activity_id_has_the_same_cache_key(email_request: dict) -> None:
    import copy

    from ghost_worker.extract.prompt import build_user_prompt
    from ghost_worker.models import ExtractRequest

    first = copy.deepcopy(email_request)
    second = copy.deepcopy(email_request)
    first["activity"]["id"] = "1e32b30c-3892-5e61-9dd5-411dc5392ac5"
    second["activity"]["id"] = "9d0f4a52-61a3-4c0e-8d77-2b1c0a5f3e11"  # Play re-ingests Event N under a new id
    a = build_user_prompt(ExtractRequest.model_validate(first))
    b = build_user_prompt(ExtractRequest.model_validate(second))
    assert a != b  # the nonce (seeded by the id) differs in the prompt that is sent...
    ca, ids_a = canonicalize(a, [])
    cb, ids_b = canonicalize(b, [])
    assert ca == cb  # ...and not in the key's text
    assert "TEXT-<<nonce>>" in ca


def test_two_runs_that_differ_only_by_the_extraction_nonce_replay_one_recording(tmp_path: Path) -> None:
    up = Upstream()
    p = provider(tmp_path, up)
    ask(p, user="Activity: x\n<<<TEXT-3fa638d17ef1c9c5\nthe email\nTEXT-3fa638d17ef1c9c5>>>")
    ask(p, user="Activity: x\n<<<TEXT-6ca5e9b94dbd0000\nthe email\nTEXT-6ca5e9b94dbd0000>>>")
    assert up.calls == 1


def test_the_nonce_is_masked_in_a_json_encoded_tool_turn_where_it_follows_an_escaped_newline() -> None:
    """The planner's tool loop is cached as complete_turn: its messages are JSON-encoded first, so STATE-<hex> follows a literal backslash-n."""
    messages = [{"role": "user", "content": "state header\nSTATE-98ec39bbb48c5524>>>\n\nGuidance"}]
    other = [{"role": "user", "content": "state header\nSTATE-b3fb85df80d3deb1>>>\n\nGuidance"}]
    from ghost_worker.llm.cache_provider import _json

    a, _ = canonicalize(_json(messages), [])
    b, _ = canonicalize(_json(other), [])
    assert a == b and "STATE-<<nonce>>" in a


def test_a_planner_turn_replays_across_runs_that_differ_only_by_the_state_nonce(tmp_path: Path) -> None:
    class Turns(Upstream):
        def complete_turn(self, **kw: Any) -> TurnResult:
            with self._lock:
                self.calls += 1
            return TurnResult(tool_calls=(), content={"n": 1}, model="live/model-x")

    up = Turns()
    p = provider(tmp_path, up)
    for nonce in ("98ec39bbb48c5524", "b3fb85df80d3deb1"):
        p.complete_turn(system="sys", messages=[{"role": "user", "content": f"header\nSTATE-{nonce}>>>"}], tools=[], schema=SCHEMA, schema_name="s")
    assert up.calls == 1


def test_wall_clock_timestamps_of_a_pulled_packet_are_masked_and_world_time_is_not() -> None:
    from ghost_worker.llm.cache_provider import _json

    def turn(created: str, computed: str, occurred: str) -> str:
        return _json([{"role": "tool", "content": f'{{"created_at": "{created}", "computed_at": "{computed}", "occurred_at": "{occurred}"}}'}])

    a, _ = canonicalize(turn("2026-10-06T09:30:22.7576439Z", "2026-10-06T09:30:20.342556Z", "2023-11-09T09:30:00Z"), [])
    b, _ = canonicalize(turn("2026-10-06T11:27:01.8672949Z", "2026-10-06T11:27:00.1Z", "2023-11-09T09:30:00Z"), [])
    c, _ = canonicalize(turn("2026-10-06T11:27:01.8672949Z", "2026-10-06T11:27:00.1Z", "2023-11-10T09:30:00Z"), [])
    assert a == b and "<<ts>>" in a and "2023-11-09T09:30:00Z" in a
    assert a != c  # a different world time is a different request


def test_a_planner_turn_replays_across_runs_that_differ_only_by_wall_clock_timestamps(tmp_path: Path) -> None:
    class Turns(Upstream):
        def complete_turn(self, **kw: Any) -> TurnResult:
            with self._lock:
                self.calls += 1
            return TurnResult(tool_calls=(), content={"n": 1}, model="live/model-x")

    up = Turns()
    p = provider(tmp_path, up)
    for stamp in ("2026-10-06T09:30:22.7576439Z", "2026-10-06T11:27:01.8672949Z"):
        p.complete_turn(system="sys", messages=[{"role": "tool", "content": f'{{"created_at": "{stamp}"}}'}], tools=[], schema=SCHEMA, schema_name="s")
    assert up.calls == 1


def test_tool_call_ids_are_masked_by_order_of_appearance_in_the_call_and_in_its_result() -> None:
    from ghost_worker.llm.cache_provider import _json

    def loop(first: str, second: str) -> str:
        return _json([
            {"role": "assistant", "content": None, "tool_calls": [{"id": first, "type": "function", "function": {"name": "state", "arguments": "{}"}},
                                                                    {"id": second, "type": "function", "function": {"name": "people", "arguments": "{}"}}]},
            {"role": "tool", "tool_call_id": first, "content": "{}"},
            {"role": "tool", "tool_call_id": second, "content": "{}"},
        ])

    a, _ = canonicalize(loop("call_2100622cef9d487789e32ff4", "call_8475efebf88e4c3a81fd217b"), [])
    b, _ = canonicalize(loop("call_18fe4f7a93974b0b8ec85f68", "call_0c2d9a1b7e3f4a5d8b6c9e10"), [])
    assert a == b and "<<call-0>>" in a and "<<call-1>>" in a
    # a result that answers the wrong call is a different request (the masking keeps the pairing)
    swapped = _json([{"role": "assistant", "tool_calls": [{"id": "call_aaaaaaaaaaaaaaaa"}, {"id": "call_bbbbbbbbbbbbbbbb"}]},
                     {"role": "tool", "tool_call_id": "call_bbbbbbbbbbbbbbbb"}])
    straight = _json([{"role": "assistant", "tool_calls": [{"id": "call_aaaaaaaaaaaaaaaa"}, {"id": "call_bbbbbbbbbbbbbbbb"}]},
                      {"role": "tool", "tool_call_id": "call_aaaaaaaaaaaaaaaa"}])
    assert canonicalize(swapped, [])[0] != canonicalize(straight, [])[0]


def test_a_planner_turn_replays_when_only_the_provider_tool_call_ids_differ(tmp_path: Path) -> None:
    class Turns(Upstream):
        def complete_turn(self, **kw: Any) -> TurnResult:
            with self._lock:
                self.calls += 1
            return TurnResult(tool_calls=(), content={"n": 1}, model="live/model-x")

    up = Turns()
    p = provider(tmp_path, up)
    for cid in ("call_2100622cef9d487789e32ff4", "call_18fe4f7a93974b0b8ec85f68"):
        p.complete_turn(system="sys", messages=[{"role": "assistant", "tool_calls": [{"id": cid}]}, {"role": "tool", "tool_call_id": cid, "content": "{}"}],
                        tools=[], schema=SCHEMA, schema_name="s")
    assert up.calls == 1
