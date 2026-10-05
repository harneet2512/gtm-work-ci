"""scripts/codespace: the CRMArena fetch + hash verification (pure logic)."""
from __future__ import annotations

import hashlib
import json
import sys
from datetime import datetime, timezone
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "codespace"))
sys.path.insert(0, str(ROOT / "bench" / "data"))

import crmarena_export as ex  # noqa: E402
import fetch_crmarena as fc  # noqa: E402
from test_bench_crmarena_export import FakeOrg  # noqa: E402

DATA = {
    "Account": [{"Id": "001B", "Name": "Beta"}, {"Id": "001A", "Name": "Alpha"}],
    "Opportunity": [{"Id": "006A", "Name": "Deal", "AccountId": "001A"}],
}
OBJECTS = tuple(DATA)
NOW = datetime(2026, 10, 3, 2, 54, 6, tzinfo=timezone.utc)


@pytest.fixture()
def pinned(tmp_path: Path) -> tuple[Path, str]:
    """A pinned manifest and export hash made from a reference export of the fake org."""
    ref = tmp_path / "ref"
    ex.export(FakeOrg(DATA), ref, objects=OBJECTS, now=NOW)
    return ref / "manifest.json", fc.export_hash(ref)


def fake_fetch(out: Path, pinned_manifest: Path, expected: str, data: dict) -> str:
    original = ex.export
    # fetch() calls crmarena_export.export with the production object list; the fake org only has two objects.
    ex.export = lambda sf, o, objects=OBJECTS, now=None: original(sf, o, objects=OBJECTS, now=now)
    try:
        return fc.fetch(out, connect=lambda: FakeOrg(data), expected=expected, pinned=pinned_manifest)
    finally:
        ex.export = original


class TestFetch:
    def test_unchanged_org_reproduces_the_pinned_hash_and_keeps_the_pinned_manifest(self, tmp_path, pinned):
        manifest, expected = pinned
        out = tmp_path / "out"
        assert fake_fetch(out, manifest, expected, DATA) == expected
        assert (out / "manifest.json").read_bytes() == manifest.read_bytes()

    def test_changed_org_is_a_hash_mismatch_naming_both_hashes(self, tmp_path, pinned):
        manifest, expected = pinned
        changed = {**DATA, "Account": DATA["Account"] + [{"Id": "001C", "Name": "Gamma"}]}
        with pytest.raises(fc.HashMismatch) as err:
            fake_fetch(tmp_path / "out", manifest, expected, changed)
        assert expected in str(err.value)

    def test_a_verified_directory_is_not_fetched_again(self, tmp_path, pinned):
        manifest, expected = pinned
        out = tmp_path / "out"
        fake_fetch(out, manifest, expected, DATA)

        def boom() -> object:
            raise AssertionError("must not connect when the data is already verified")

        assert fc.fetch(out, connect=boom, expected=expected, pinned=manifest) == expected

    def test_is_present_is_false_for_missing_empty_and_tampered_directories(self, tmp_path, pinned):
        manifest, expected = pinned
        assert not fc.is_present(tmp_path / "missing", expected)
        (tmp_path / "empty").mkdir()
        assert not fc.is_present(tmp_path / "empty", expected)
        out = tmp_path / "out"
        fake_fetch(out, manifest, expected, DATA)
        (out / "Account.json").write_text("[]\n", encoding="utf-8")
        assert not fc.is_present(out, expected)

    def test_main_exit_codes(self, tmp_path, pinned, monkeypatch, capsys):
        manifest, expected = pinned
        out = tmp_path / "out"
        fake_fetch(out, manifest, expected, DATA)
        monkeypatch.setattr(fc, "EXPECTED_EXPORT_SHA256", expected)
        assert fc.main(["--out", str(out)]) == 0
        assert "already present" in capsys.readouterr().out
        (out / "Opportunity.json").write_text("[]\n", encoding="utf-8")

        def mismatch(_out: Path) -> str:
            raise fc.HashMismatch("export hash x is not the pinned y")

        monkeypatch.setattr(fc, "fetch", mismatch)
        assert fc.main(["--out", str(out)]) == 2
        assert "upload the snapshot instead" in capsys.readouterr().err

        def broken(_out: Path) -> str:
            raise ex.ExportError("login to the CRMArena B2B org failed (X)")

        monkeypatch.setattr(fc, "fetch", broken)
        assert fc.main(["--out", str(out)]) == 1

    def test_export_hash_matches_the_definition_the_synthetic_layer_uses(self, tmp_path, pinned):
        manifest, _ = pinned
        digest = hashlib.sha256()
        ref = manifest.parent
        for path in sorted(ref.glob("*.json")):
            digest.update(path.name.encode())
            digest.update(hashlib.sha256(path.read_bytes()).digest())
        assert fc.export_hash(ref) == digest.hexdigest()

    def test_the_committed_pin_is_the_snapshot_the_demo_report_was_mined_on(self):
        report = json.loads((ROOT / "bench" / "reports" / "demo-cases-2026-10-04.json").read_text(encoding="utf-8"))
        assert hashlib.sha256(fc.PINNED_MANIFEST.read_bytes()).hexdigest() == report["snapshot"]["manifest_sha256"]
        pinned_doc = json.loads(fc.PINNED_MANIFEST.read_text(encoding="utf-8"))
        assert set(pinned_doc["sha256"]) == {f"{name}.json" for name in ex.OBJECTS}
        assert fc.EXPECTED_EXPORT_SHA256.startswith("b553e9a3")
        synthetic = json.loads((ROOT / "bench" / "reports" / "synthetic-generated-v1-manifest.json").read_text(encoding="utf-8"))
        assert synthetic["base_export_sha256"] == fc.EXPECTED_EXPORT_SHA256
