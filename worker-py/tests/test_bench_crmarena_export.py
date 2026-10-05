"""bench/data/crmarena_export.py: pure parts and idempotency, against a fake read-only org."""
from __future__ import annotations

import json
import sys
from datetime import datetime, timezone
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench" / "data"))

import crmarena_export as ex  # noqa: E402

README = """# CRMArena-Pro B2B
SALESFORCE_B2B_USERNAME=user@example.org
SALESFORCE_B2B_PASSWORD=pw-value
SALESFORCE_B2B_SECURITY_TOKEN=tok-value
SALESFORCE_B2C_USERNAME=other@example.org
"""


class FakeOrg:
    """Answers describe and SELECT only; records every SOQL statement it was given."""

    def __init__(self, data: dict[str, list[dict]]) -> None:
        self.data = data
        self.soql: list[str] = []

    def describe_fields(self, sobject: str) -> list[dict]:
        names = sorted({k for r in self.data[sobject] for k in r if k != "attributes"})
        return [{"name": n, "type": "string"} for n in names] + [
            {"name": "BillingAddress", "type": "address"}, {"name": "LastViewedDate", "type": "datetime"},
            {"name": "UserPreferencesX", "type": "boolean"}]

    def query_all(self, soql: str) -> list[dict]:
        self.soql.append(soql)
        sobject = soql.rsplit(" FROM ", 1)[1]
        return [dict(r, attributes={"type": sobject}) for r in reversed(self.data[sobject])]


DATA = {"Account": [{"Id": "001A", "Name": "Acme"}, {"Id": "001B", "Name": "Beta"}],
        "Contact": [{"Id": "003A", "AccountId": "001A", "Email": "a@acme.example"}]}
T0 = datetime(2026, 10, 2, 12, 0, tzinfo=timezone.utc)
T1 = datetime(2026, 10, 3, 12, 0, tzinfo=timezone.utc)


def test_parse_credentials_reads_only_the_b2b_block() -> None:
    creds = ex.parse_credentials(README)
    assert creds == {"USERNAME": "user@example.org", "PASSWORD": "pw-value", "SECURITY_TOKEN": "tok-value"}


def test_parse_credentials_error_never_echoes_values() -> None:
    with pytest.raises(ex.ExportError) as err:
        ex.parse_credentials("SALESFORCE_B2B_PASSWORD=pw-value\n")
    assert "pw-value" not in str(err.value)


def test_exported_fields_drop_compound_volatile_and_preference_fields() -> None:
    fields = ex.exported_fields(FakeOrg(DATA).describe_fields("Account"))
    assert fields == ["Id", "Name"]


def test_clean_drops_attributes_and_sorts_by_id_without_mutating_input() -> None:
    raw = [{"Id": "2", "attributes": {}}, {"Id": "1", "attributes": {}}]
    assert ex.clean(raw) == [{"Id": "1"}, {"Id": "2"}]
    assert "attributes" in raw[0]


def test_export_writes_files_counts_hashes_and_only_selects(tmp_path: Path) -> None:
    org = FakeOrg(DATA)
    manifest = ex.export(org, tmp_path, objects=("Account", "Contact"), now=T0)
    assert manifest["counts"] == {"Account": 2, "Contact": 1}
    assert manifest["licence"].startswith("CC BY-NC 4.0")
    assert manifest["exported_at"] == "2026-10-02T12:00:00Z"
    accounts = json.loads((tmp_path / "Account.json").read_text(encoding="utf-8"))
    assert [a["Id"] for a in accounts] == ["001A", "001B"]
    assert manifest["sha256"]["Account.json"] == ex.sha256((tmp_path / "Account.json").read_bytes())
    assert all(s.startswith("SELECT ") for s in org.soql)


def test_rerun_on_unchanged_org_is_idempotent(tmp_path: Path) -> None:
    first = ex.export(FakeOrg(DATA), tmp_path, objects=("Account", "Contact"), now=T0)
    before = {p.name: p.stat().st_mtime_ns for p in tmp_path.iterdir()}
    second = ex.export(FakeOrg(DATA), tmp_path, objects=("Account", "Contact"), now=T1)
    assert second == first  # export time kept: nothing changed
    assert {p.name: p.stat().st_mtime_ns for p in tmp_path.iterdir()} == before


def test_changed_org_rewrites_manifest(tmp_path: Path) -> None:
    ex.export(FakeOrg(DATA), tmp_path, objects=("Account", "Contact"), now=T0)
    changed = {**DATA, "Account": DATA["Account"] + [{"Id": "001C", "Name": "Gamma"}]}
    manifest = ex.export(FakeOrg(changed), tmp_path, objects=("Account", "Contact"), now=T1)
    assert manifest["counts"]["Account"] == 3
    assert manifest["exported_at"] == "2026-10-03T12:00:00Z"


def test_object_without_id_is_rejected(tmp_path: Path) -> None:
    with pytest.raises(ex.ExportError):
        ex.export(FakeOrg({"Thing": [{"Name": "x"}]}), tmp_path, objects=("Thing",), now=T0)
