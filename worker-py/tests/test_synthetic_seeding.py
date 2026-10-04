"""WP32 (HAR-131): deterministic seeding API and record markers of the synthetic layer generator."""
from __future__ import annotations

import re
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import generate, records, rules, seeding  # noqa: E402


def draws(s: seeding.Stream, n: int = 5) -> list[float]:
    return [s.uniform() for _ in range(n)]


def test_same_root_and_path_give_the_same_sequence() -> None:
    assert draws(seeding.stream(7, "account", "001A", "reorg")) == draws(seeding.stream(7, "account", "001A", "reorg"))


def test_different_root_or_path_give_different_sequences() -> None:
    base = draws(seeding.stream(7, "account", "001A", "reorg"))
    assert base != draws(seeding.stream(8, "account", "001A", "reorg"))
    assert base != draws(seeding.stream(7, "account", "001B", "reorg"))
    assert base != draws(seeding.stream(7, "account", "001A", "champion"))


def test_streams_are_isolated_so_adding_a_draw_elsewhere_changes_nothing() -> None:
    a1 = seeding.stream(7, "deal", "006X", "replies")
    draws(seeding.stream(7, "deal", "006X", "outcome"), 50)
    assert draws(a1) == draws(seeding.stream(7, "deal", "006X", "replies"))


def test_path_parts_are_not_ambiguous_when_joined() -> None:
    assert seeding.derive_seed(1, "ab", "c") != seeding.derive_seed(1, "a", "bc")


@pytest.mark.parametrize("bad", [1.5, "7", True])
def test_root_seed_must_be_an_int(bad: object) -> None:
    with pytest.raises(TypeError):
        seeding.derive_seed(bad, "x")  # type: ignore[arg-type]


def test_golden_values_pin_the_algorithm_across_python_versions() -> None:
    """random.Random.random() with an int seed is the one sequence CPython guarantees to keep."""
    assert seeding.derive_seed(20261002, "golden") == 18239375769770143466
    assert [round(x, 12) for x in draws(seeding.stream(20261002, "golden"), 3)] == GOLDEN


GOLDEN = [0.991648416821, 0.681157005916, 0.003723853005]  # measured 2026-10-02, CPython 3.12+


def test_helpers_are_built_only_on_uniform_draws() -> None:
    s = seeding.stream(3, "helpers")
    assert all(s.bernoulli(1.0) for _ in range(5)) and not any(s.bernoulli(0.0) for _ in range(5))
    assert {s.integer(2, 4) for _ in range(200)} == {2, 3, 4}
    assert s.choice(["only"]) == "only"
    xs = [s.normal(0.0, 1.0) for _ in range(4000)]
    mean = sum(xs) / len(xs)
    assert abs(mean) < 0.08 and 0.9 < (sum((x - mean) ** 2 for x in xs) / len(xs)) ** 0.5 < 1.1
    assert sorted(s.shuffled([1, 2, 3, 4])) == [1, 2, 3, 4]


@pytest.mark.parametrize("bad", [-0.1, 1.1])
def test_bernoulli_rejects_probabilities_outside_unit_interval(bad: float) -> None:
    with pytest.raises(ValueError):
        seeding.stream(1, "x").bernoulli(bad)


def test_mark_returns_a_new_record_with_origin_and_provenance() -> None:
    rec = {"source_system": "email", "source_object_id": "<syn1-reply-1@acme.example>"}
    marked = records.mark(rec)
    assert marked == {**rec, "origin": "synthetic", "provenance": "synthetic:v1"}
    assert "origin" not in rec  # input untouched


def test_mark_refuses_to_relabel_a_record_of_another_origin() -> None:
    with pytest.raises(ValueError):
        records.mark({"origin": "dataset", "provenance": "crmarena-pro:b2b"})


SF_ID = re.compile(r"^(003|006|02s|002|00T)Wt0000[0-9A-Za-z]{6}[A-Z0-5]{3}$")


@pytest.mark.parametrize("real", ["00TWt000002zEpkMAE", "006Wt000007BAMjIAO", "00TWt000002ysKqMAI"])
def test_checksum_matches_real_crmarena_ids(real: str) -> None:
    assert records.sf_checksum(real[:15]) == real[15:]


def test_synthetic_ids_look_exactly_like_base_ids_and_carry_no_marker() -> None:
    a = records.sf_id("Contact", "001Wt00000ABCDEIAA", "entrant", "1")
    assert a == records.sf_id("Contact", "001Wt00000ABCDEIAA", "entrant", "1")
    assert a != records.sf_id("Contact", "001Wt00000ABCDEIAA", "entrant", "2")
    assert SF_ID.match(a) and records.sf_checksum(a[:15]) == a[15:]
    assert "syn" not in a.lower() and SF_ID.match(records.sf_id("EmailMessage", "x"))
    assert records.message_id(a, "acme.com") == f"<{a}@acme.com>"


def test_generation_is_refused_while_the_rule_set_is_a_draft(tmp_path: Path) -> None:
    import copy
    import json

    doc = copy.deepcopy(rules.load_doc())
    doc["status"] = "draft"
    doc.pop("frozen_sha256", None)
    path = tmp_path / "rules.v1.json"
    path.write_text(json.dumps(doc), encoding="utf-8")
    assert any("spec review" in r for r in generate.preflight(rules_path=path))


def test_the_committed_rule_set_is_frozen_and_only_the_sweep_gate_can_block() -> None:
    assert rules.load_rules().status == "frozen"
    assert all(r.startswith("sweep gate: ") for r in generate.preflight())  # frozen hash and report are current


def test_there_is_no_seed_option_generation_uses_the_frozen_seed() -> None:
    with pytest.raises(SystemExit):
        generate.parse(["--seed", "1", "--out", "x"])
