"""bench/data/crmarena_replay_worker.py: nonce-masked and approximate replay of recorded cassettes, never a live call."""
from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench" / "data"))

import crmarena_replay_worker as rw  # noqa: E402
from ghost_worker.errors import CassetteNotFoundError  # noqa: E402
from ghost_worker.extract.schema import OUTPUT_SCHEMA, OUTPUT_SCHEMA_NAME  # noqa: E402
from ghost_worker.llm.fake_provider import schema_sha256  # noqa: E402

SYSTEM = "system prompt"


def prompt(nonce: str, people: str = "- a@x.com | A | - | buyer", text: str = "Hello there") -> str:
    return (f"Activity: type=EmailSent source=email occurred_at=2024-01-01T00:00:00Z\nParticipants:\n- a@x.com | A | from | buyer\n"
            f"Known people:\n{people}\n\n<<<TEXT-{nonce}\n{text}\nTEXT-{nonce}>>>")


def cassette_dir(tmp_path: Path, user: str) -> Path:
    d = tmp_path / "c"
    d.mkdir()
    doc = {"request": {"system": SYSTEM, "user": user, "schema_name": OUTPUT_SCHEMA_NAME, "schema_sha256": schema_sha256(OUTPUT_SCHEMA)},
           "response": {"content": {"claims": []}, "model": "m", "usage": {}}}
    (d / "k.json").write_text(json.dumps(doc), encoding="utf-8")
    return d


def ask(provider: rw.MaskedReplayProvider, user: str):
    return provider.complete_json(system=SYSTEM, user=user, schema=OUTPUT_SCHEMA, schema_name=OUTPUT_SCHEMA_NAME)


def test_a_different_nonce_still_hits_exactly(tmp_path: Path) -> None:
    provider = rw.MaskedReplayProvider(cassette_dir(tmp_path, prompt("0123456789abcdef")))
    assert ask(provider, prompt("fedcba9876543210")).model == "m"
    assert (provider.exact_hits, provider.approximate_hits) == (1, 0)


def test_a_different_known_people_block_is_an_approximate_hit(tmp_path: Path) -> None:
    provider = rw.MaskedReplayProvider(cassette_dir(tmp_path, prompt("0123456789abcdef")))
    assert ask(provider, prompt("fedcba9876543210", people="- a@x.com | A | - | seller")).model == "m"
    assert (provider.exact_hits, provider.approximate_hits) == (0, 1)


def test_a_different_email_is_a_miss_and_never_a_live_call(tmp_path: Path) -> None:
    misses = tmp_path / "misses"
    provider = rw.MaskedReplayProvider(cassette_dir(tmp_path, prompt("0123456789abcdef")), dump_misses=misses)
    with pytest.raises(CassetteNotFoundError):
        ask(provider, prompt("fedcba9876543210", text="A different email"))
    assert len(list(misses.glob("*.txt"))) == 1 and (provider.exact_hits, provider.approximate_hits) == (0, 0)


def test_tool_turns_are_refused() -> None:
    with pytest.raises(NotImplementedError):
        rw.MaskedReplayProvider.complete_turn(object())


def test_approximate_hits_are_logged_with_the_activity_they_served(tmp_path: Path) -> None:
    provider = rw.MaskedReplayProvider(cassette_dir(tmp_path, prompt("0123456789abcdef")))
    ask(provider, prompt("fedcba9876543210"))  # exact
    ask(provider, prompt("fedcba9876543210", people="- a@x.com | A | - | seller"))  # approximate
    assert len(provider.approximate_log) == 1
    entry = provider.approximate_log[0]
    assert entry["activity"] == "Activity: type=EmailSent source=email occurred_at=2024-01-01T00:00:00Z"
    assert len(entry["loose_key"]) == 12
    stats = rw.replay_stats(provider)
    assert stats["approximate_hits"] == 1 and stats["exact_hits"] == 1
    assert "approximate_log" not in stats  # polled once per extraction: the log has its own route
