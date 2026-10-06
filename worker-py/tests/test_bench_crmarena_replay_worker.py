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


# --- known misses (HAR-124) ---------------------------------------------------------------------

def known_miss_dir(tmp_path: Path, user: str) -> Path:
    import known_misses as km

    d = tmp_path / "misses"
    d.mkdir()
    system, name, sha = km._context()
    (d / f"{rw.replay_key(system, user, name, sha)}.txt").write_text(user, encoding="utf-8")
    return d


def ask_real(provider: rw.MaskedReplayProvider, user: str):
    import known_misses as km

    system, name, _ = km._context()
    return provider.complete_json(system=system, user=user, schema=OUTPUT_SCHEMA, schema_name=name)


def test_a_known_miss_is_answered_with_no_claims_even_when_the_people_block_differs(tmp_path: Path) -> None:
    import known_misses as km

    user = prompt("0123456789abcdef", text="A mined miss")
    provider = rw.MaskedReplayProvider(cassette_dir(tmp_path, prompt("0123456789abcdef")), known_misses=km.load_loose_keys(known_miss_dir(tmp_path, user)))
    for people in ("- a@x.com | A | - | buyer", "- a@x.com | A | - | seller"):
        result = ask_real(provider, prompt("fedcba9876543210", people=people, text="A mined miss"))
        assert result.content == {"claims": []} and result.model == rw.KNOWN_MISS_MODEL
    assert provider.known_miss_hits == 2 and rw.replay_stats(provider)["known_misses"] == 1


def test_an_unknown_miss_still_raises_when_others_are_allowlisted(tmp_path: Path) -> None:
    import known_misses as km

    provider = rw.MaskedReplayProvider(cassette_dir(tmp_path, prompt("0123456789abcdef")),
                                       known_misses=km.load_loose_keys(known_miss_dir(tmp_path, prompt("0123456789abcdef", text="A mined miss"))))
    with pytest.raises(CassetteNotFoundError):
        ask_real(provider, prompt("fedcba9876543210", text="A brand new email"))
    assert provider.known_miss_hits == 0


def test_a_dump_from_another_prompt_is_refused(tmp_path: Path) -> None:
    import known_misses as km

    d = tmp_path / "stale"
    d.mkdir()
    (d / ("0" * 64 + ".txt")).write_text(prompt("0123456789abcdef"), encoding="utf-8")
    with pytest.raises(ValueError, match="not a miss of the current prompt"):
        km.load_loose_keys(d)


def test_the_checked_in_manifest_loads_and_matches_the_directory_form(tmp_path: Path) -> None:
    import known_misses as km

    d = known_miss_dir(tmp_path, prompt("0123456789abcdef", text="A mined miss"))
    out = tmp_path / "m.json"
    out.write_text(json.dumps(km.build_manifest(d)), encoding="utf-8")
    assert km.load_loose_keys(out) == km.load_loose_keys(d)
    assert len(km.load_loose_keys(km.DEFAULT_MANIFEST)) == 33


def test_a_manifest_under_another_prompt_or_with_a_wrong_count_is_refused(tmp_path: Path) -> None:
    import known_misses as km

    good = json.loads(km.DEFAULT_MANIFEST.read_text(encoding="utf-8"))
    for change in ({"prompt_sha256": "x"}, {"count": 1}, {"version": "v0"}):
        bad = tmp_path / "bad.json"
        bad.write_text(json.dumps({**good, **change}), encoding="utf-8")
        with pytest.raises(ValueError):
            km.load_loose_keys(bad)


def test_the_same_miss_outside_the_setup_is_an_error(tmp_path: Path) -> None:
    """Only a worker started by the setup with the allowlist answers a known miss with no claims; any other worker raises."""
    provider = rw.MaskedReplayProvider(cassette_dir(tmp_path, prompt("0123456789abcdef")))
    with pytest.raises(CassetteNotFoundError):
        ask_real(provider, prompt("fedcba9876543210", text="A mined miss"))
    assert provider.known_miss_hits == 0


def test_a_new_dump_in_the_misses_folder_is_not_allowed(tmp_path: Path) -> None:
    """The misses folder is diagnostic output: the worker takes only the checked-in manifest, and a dump written later allows nothing."""
    import known_misses as km

    shipped = km.load_loose_keys(km.DEFAULT_MANIFEST)
    provider = rw.MaskedReplayProvider(cassette_dir(tmp_path, prompt("0123456789abcdef")), dump_misses=tmp_path / "misses", known_misses=shipped)
    fresh = prompt("fedcba9876543210", text="A brand new email nobody allowlisted")
    with pytest.raises(CassetteNotFoundError):
        ask_real(provider, fresh)
    assert len(list((tmp_path / "misses").glob("*.txt"))) == 1  # the dump exists, and still allows nothing
    with pytest.raises(CassetteNotFoundError):
        ask_real(provider, fresh)
    assert provider.known_miss_hits == 0
