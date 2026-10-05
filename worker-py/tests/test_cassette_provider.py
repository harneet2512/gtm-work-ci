from __future__ import annotations

import json
import logging
import os
from pathlib import Path

import pytest

from ghost_worker.errors import CassetteNotFoundError, InvalidModelOutputError, ProviderError
from ghost_worker.llm.fake_provider import (
    FakeProvider,
    RecordingProvider,
    cassette_key,
    model_family,
    write_cassette,
)
from ghost_worker.llm.provider import LLMResult

SCHEMA = {"type": "object"}
KWARGS = {"system": "sys", "user": "usr", "schema": SCHEMA, "schema_name": "thing"}
KEY = cassette_key("fam", "sys", "usr", "thing", SCHEMA)


class Upstream:
    def __init__(self, content: dict | Exception, model: str = "deepseek/deepseek-v4-flash") -> None:
        self.content = content
        self.model = model
        self.calls = 0

    def complete_json(self, **kwargs: object) -> LLMResult:
        self.calls += 1
        if isinstance(self.content, Exception):
            raise self.content
        return LLMResult(content=self.content, model=self.model, usage={"total_tokens": 5})


def test_model_family_strips_provider_prefix_and_tag() -> None:
    assert model_family("openrouter/deepseek/deepseek-v4-flash") == "deepseek-v4-flash"
    assert model_family("openrouter/dots-studio/dots-3-note-preview:free") == "dots-3-note-preview"
    assert model_family("plain") == "plain"


def test_cassette_key_is_sha256_of_canonical_json() -> None:
    assert len(KEY) == 64 and int(KEY, 16) >= 0
    assert KEY == cassette_key("fam", "sys", "usr", "thing", {"type": "object"})
    for other in (("fam2", "sys", "usr", "thing"), ("fam", "sys2", "usr", "thing"),
                  ("fam", "sys", "usr2", "thing"), ("fam", "sys", "usr", "other")):
        assert cassette_key(*other, SCHEMA) != KEY


def test_cassette_key_depends_on_schema_and_ignores_key_order() -> None:
    assert cassette_key("f", "s", "u", "t", {"type": "object", "a": 1}) == \
        cassette_key("f", "s", "u", "t", {"a": 1, "type": "object"})
    assert cassette_key("f", "s", "u", "t", {"type": "object"}) != \
        cassette_key("f", "s", "u", "t", {"type": "object", "required": ["x"]})


def test_cassette_key_handles_unicode() -> None:
    assert cassette_key("f", "s", "café \U0001F600", "t", SCHEMA) != cassette_key("f", "s", "cafe", "t", SCHEMA)


def test_record_then_replay_round_trip(tmp_path: Path) -> None:
    upstream = Upstream({"claims": [1]})
    recorded = RecordingProvider(upstream, tmp_path, "fam").complete_json(**KWARGS)
    path = tmp_path / f"{KEY}.json"
    assert path.exists()
    body = json.loads(path.read_text(encoding="utf-8"))
    assert body["key"] == KEY
    assert body["request"]["schema_name"] == "thing"
    assert not list(tmp_path.glob("*.tmp"))

    replayed = FakeProvider(tmp_path, "fam").complete_json(**KWARGS)
    assert replayed == recorded
    assert upstream.calls == 1


def test_record_stores_the_actual_responding_model(tmp_path: Path) -> None:
    RecordingProvider(Upstream({"x": 1}, model="dots-3-note-preview"), tmp_path, "fam").complete_json(**KWARGS)
    body = json.loads((tmp_path / f"{KEY}.json").read_text(encoding="utf-8"))
    assert body["response"]["model"] == "dots-3-note-preview"
    assert FakeProvider(tmp_path, "fam").complete_json(**KWARGS).model == "dots-3-note-preview"


def test_record_warns_that_cassettes_may_contain_customer_text(
        tmp_path: Path, caplog: pytest.LogCaptureFixture) -> None:
    with caplog.at_level(logging.WARNING, logger="ghost_worker"):
        RecordingProvider(Upstream({"x": 1}), tmp_path, "fam").complete_json(**KWARGS)
    assert any("customer" in r.getMessage().lower() for r in caplog.records)


def test_schema_change_misses_cassette(tmp_path: Path) -> None:
    RecordingProvider(Upstream({"x": 1}), tmp_path, "fam").complete_json(**KWARGS)
    with pytest.raises(CassetteNotFoundError):
        FakeProvider(tmp_path, "fam").complete_json(**{**KWARGS, "schema": {"type": "object", "x": 1}})


def test_replay_missing_cassette_raises_clear_error(tmp_path: Path) -> None:
    with pytest.raises(CassetteNotFoundError) as info:
        FakeProvider(tmp_path, "fam").complete_json(**KWARGS)
    assert KEY in str(info.value)
    assert "GHOST_LLM_MODE=record" in str(info.value)


def test_replay_corrupt_cassette_raises(tmp_path: Path) -> None:
    (tmp_path / f"{KEY}.json").write_text("{not json", encoding="utf-8")
    with pytest.raises(InvalidModelOutputError):
        FakeProvider(tmp_path, "fam").complete_json(**KWARGS)


def test_replay_cassette_missing_response_raises(tmp_path: Path) -> None:
    (tmp_path / f"{KEY}.json").write_text("{}", encoding="utf-8")
    with pytest.raises(InvalidModelOutputError):
        FakeProvider(tmp_path, "fam").complete_json(**KWARGS)


def test_different_prompt_misses_cassette(tmp_path: Path) -> None:
    RecordingProvider(Upstream({"x": 1}), tmp_path, "fam").complete_json(**KWARGS)
    with pytest.raises(CassetteNotFoundError):
        FakeProvider(tmp_path, "fam").complete_json(**{**KWARGS, "user": "changed"})


def test_record_does_not_write_on_upstream_failure(tmp_path: Path) -> None:
    recorder = RecordingProvider(Upstream(ProviderError("down")), tmp_path, "fam")
    with pytest.raises(ProviderError):
        recorder.complete_json(**KWARGS)
    assert list(tmp_path.iterdir()) == []


def test_record_creates_missing_directory(tmp_path: Path) -> None:
    target = tmp_path / "nested" / "cassettes"
    RecordingProvider(Upstream({"x": 1}), target, "fam").complete_json(**KWARGS)
    assert len(list(target.glob("*.json"))) == 1


def test_failed_atomic_write_leaves_no_partial_files(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    def fail(*_: object) -> None:
        raise OSError("disk full")

    monkeypatch.setattr(os, "replace", fail)
    with pytest.raises(OSError):
        write_cassette(tmp_path, "k", {"a": 1})
    assert list(tmp_path.iterdir()) == []


def test_record_overwrites_atomically(tmp_path: Path) -> None:
    RecordingProvider(Upstream({"v": 1}), tmp_path, "fam").complete_json(**KWARGS)
    RecordingProvider(Upstream({"v": 2}), tmp_path, "fam").complete_json(**KWARGS)
    assert FakeProvider(tmp_path, "fam").complete_json(**KWARGS).content == {"v": 2}
    assert len(list(tmp_path.iterdir())) == 1
