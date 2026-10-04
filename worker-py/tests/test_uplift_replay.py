"""The committed A/B/C report is valid, pinned to its inputs and reproducible from its cassettes (HAR-129 section G:
'the A/B/C proof is reproducible'). The fast checks always run once a report is committed; the full replay (Go, an
embedded Postgres, the real worker in replay mode, no network to any model) runs in the core-worker CI job
(GHOST_UPLIFT_REPLAY=1) and re-derives the report byte for byte."""
from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from bench.uplift import blind, frozen, report  # noqa: E402

REPORTS = ROOT / "bench" / "uplift" / "reports"
COMMITTED = REPORTS / "abc_report.json"

recorded = pytest.mark.skipif(not COMMITTED.exists(), reason="the recorded run is not committed yet")


def committed() -> dict:
    return json.loads(COMMITTED.read_text(encoding="utf-8"))


@recorded
def test_the_committed_report_is_valid_and_every_aggregate_follows_from_its_situations() -> None:
    assert report.verify(committed()) == []


@recorded
def test_the_report_is_pinned_to_the_frozen_inputs_and_the_cassettes_it_was_recorded_with() -> None:
    r = committed()
    manifest = frozen.read_json(frozen.MANIFEST_PATH)
    data = r["provenance"]["data"]
    assert data["pack_sha256"] == frozen.sha256_file(frozen.PACK_PATH)
    for key in ("learning_sha256", "rules_sha256", "split_sha256", "base_export_sha256", "synthetic_manifest_sha256", "rules_version"):
        assert data[key] == manifest[key], key
    assert r["provenance"]["cassettes"] == frozen.cassette_digest()
    assert r["provenance"]["seed"] == frozen.SEED == manifest["seed"]
    runtime = frozen.read_json(frozen.RUNTIME_PATH)
    assert r["provenance"]["runtime_model"]["configured"] == runtime["configured_model"]
    assert [s["id"] for s in r["situations"]] == [s["id"] for s in frozen.read_json(frozen.SITUATIONS_PATH)["situations"]
                                                  if s["id"] in set(runtime["situation_ids"])]


@recorded
def test_the_blind_pairs_are_unlabelled_and_their_key_is_a_separate_valid_file() -> None:
    pairs = [json.loads(line) for line in (REPORTS / "pairs_blind.jsonl").read_text(encoding="utf-8").splitlines()]
    key = json.loads((REPORTS / "pairs_key.json").read_text(encoding="utf-8"))
    assert blind.validate(pairs, key) == []
    text = json.dumps(pairs).lower()
    assert "knowledge" not in text and '"arm"' not in text and "@" not in text


@recorded
def test_the_report_says_what_it_does_not_prove() -> None:
    r = committed()
    assert any("does not prove the rules hold in real selling" in c for c in r["caveats"])
    assert r["headline"]["text"] and r["learning"]["lifecycle_note"]


@recorded
@pytest.mark.skipif(os.environ.get("GHOST_UPLIFT_REPLAY") != "1" or shutil.which("go") is None,
                    reason="the full replay needs Go and runs in the core-worker CI job (GHOST_UPLIFT_REPLAY=1)")
def test_replaying_the_cassettes_reproduces_the_committed_report_byte_for_byte(tmp_path: Path) -> None:
    done = subprocess.run([sys.executable, "-m", "bench.uplift", "run", "--mode", "replay", "--out-dir", str(tmp_path)], cwd=ROOT,
                          capture_output=True, text=True, check=False)
    assert done.returncode == 0, done.stdout[-2000:] + done.stderr[-2000:]
    for name in ("abc_report.json", "pairs_blind.jsonl", "pairs_key.json"):
        assert (tmp_path / name).read_bytes() == (REPORTS / name).read_bytes(), f"{name} differs after a replay"
