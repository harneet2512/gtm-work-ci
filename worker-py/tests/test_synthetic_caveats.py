"""WP32 (HAR-131): the A/B/C caveats are a constant that every report must carry, and the spec quotes the same text."""
from __future__ import annotations

import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import caveats  # noqa: E402

SPEC = (ROOT / "docs" / "data" / "synthetic-layer-v1.md").read_text(encoding="utf-8").replace("\r\n", "\n")


def test_there_are_four_caveats_and_the_block_carries_all_of_them() -> None:
    assert [k for k, _ in caveats.ABC_CAVEATS] == ["synthetic_outcomes", "distinguishable_data", "noisy_mode", "real_base_exception"]
    caveats.assert_carries_caveats(f"# A/B/C report\n\n{caveats.caveat_block()}\n")


def test_a_report_missing_a_caveat_is_refused() -> None:
    block = caveats.caveat_block().replace(caveats.ABC_CAVEATS[2][1], "")
    with pytest.raises(ValueError, match="noisy_mode"):
        caveats.assert_carries_caveats(block)
    with pytest.raises(ValueError):
        caveats.assert_carries_caveats("results: B beats A")


def test_the_spec_quotes_the_constant() -> None:
    for key, text in caveats.ABC_CAVEATS:
        assert text in SPEC, key


def test_the_caveats_state_the_current_numbers() -> None:
    import json

    real = json.loads((ROOT / "bench" / "reports" / "synthetic-power-real-v1.json").read_text(encoding="utf-8"))
    assert real["noisy"]["seed_pass_rate"] == 0.075 and real["clean"]["seed_pass_rate"] == 0.425
    assert "0.075" in caveats.ABC_CAVEATS[2][1] and "0.425" in caveats.ABC_CAVEATS[3][1]
