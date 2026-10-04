"""bench/data/crmarena_cassettes.py: the manifest pins a cassette directory by count, size and aggregate hash."""
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench" / "data"))

import crmarena_cassettes as cc  # noqa: E402


def _dir(tmp_path: Path) -> Path:
    d = tmp_path / "cassettes"
    d.mkdir()
    (d / "a.json").write_text('{"x":1}', encoding="utf-8")
    (d / "b.json").write_text('{"y":2}', encoding="utf-8")
    (d / "ignored.txt").write_text("not a cassette", encoding="utf-8")
    return d


def test_manifest_then_verify_round_trips(tmp_path: Path) -> None:
    d, manifest = _dir(tmp_path), tmp_path / "m.json"
    assert cc.main(["manifest", str(d), str(manifest)]) == 0
    data = json.loads(manifest.read_text(encoding="utf-8"))
    assert data["files"] == 2 and data["total_bytes"] == 14 and len(data["aggregate_sha256"]) == 64
    assert cc.main(["verify", str(d), str(manifest)]) == 0


def test_verify_fails_on_changed_added_or_removed_files(tmp_path: Path) -> None:
    d, manifest = _dir(tmp_path), tmp_path / "m.json"
    cc.main(["manifest", str(d), str(manifest)])
    (d / "a.json").write_text('{"x":2}', encoding="utf-8")
    assert cc.main(["verify", str(d), str(manifest)]) == 1
    (d / "a.json").write_text('{"x":1}', encoding="utf-8")
    (d / "c.json").write_text("{}", encoding="utf-8")
    assert cc.main(["verify", str(d), str(manifest)]) == 1
    (d / "c.json").unlink()
    (d / "b.json").unlink()
    assert cc.main(["verify", str(d), str(manifest)]) == 1


def test_manifest_matches_the_real_aggregate_definition(tmp_path: Path) -> None:
    import hashlib
    d = _dir(tmp_path)
    expected = hashlib.sha256()
    for name in ("a.json", "b.json"):
        expected.update(name.encode() + b":" + hashlib.sha256((d / name).read_bytes()).hexdigest().encode() + b"\n")
    assert cc.fingerprint(d)["aggregate_sha256"] == expected.hexdigest()
