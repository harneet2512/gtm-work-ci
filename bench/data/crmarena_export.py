"""Read-only export of the CRMArena-Pro B2B Salesforce org (Salesforce AI Research) for HAR-130.

    pip install simple-salesforce
    python bench/data/crmarena_export.py [--out data/crmarena_b2b]

Writes one `<Object>.json` per exported sObject (records sorted by Id, keys sorted, Salesforce
`attributes` dropped) and `manifest.json` (export time, counts, source, licence, SHA-256 per file)
into the git-ignored `data/crmarena_b2b/`. Re-running against an unchanged org rewrites nothing and
keeps the manifest (and its export time) as it was: the export is idempotent.

Access: the org credentials are published in the CRMArena README (SALESFORCE_B2B_*). They are parsed
inside this process only and are never printed, logged or written anywhere. Only SOQL queries
(`describe`, `query_all`) are issued: the script makes no write call of any kind.

Licence of the exported data: CC BY-NC 4.0 (non-commercial use only). The data is third-party
synthetic data created by Salesforce AI Research, not by us.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Protocol

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUT = ROOT / "data" / "crmarena_b2b"
SOURCE_URL = "https://raw.githubusercontent.com/SalesforceAIResearch/CRMArena/main/README.md"
DATASET = "CRMArena-Pro B2B Salesforce org (Salesforce AI Research)"
LICENCE = "CC BY-NC 4.0 (non-commercial use only)"
LICENCE_URL = "https://github.com/SalesforceAIResearch/CRMArena/blob/main/LICENSE.txt"

# Exported sObjects. The loader maps the first block; the rest back the data card's gap numbers.
OBJECTS = (
    "Account", "Contact", "User", "Opportunity", "EmailMessage", "Task", "Quote", "Order", "Contract",
    "Case", "LiveChatTranscript",
    "OpportunityHistory", "OpportunityContactRole", "OpportunityLineItem", "QuoteLineItem", "OrderItem",
    "AccountHistory", "ContactHistory", "CaseHistory", "EmailMessageRelation", "Lead", "Product2",
)
# Compound fields duplicate their component fields; view tracking changes whenever anyone opens a
# record, and HtmlBody duplicates TextBody, so none of them belong in a reproducible snapshot.
SKIP_TYPES = frozenset({"address", "location", "base64"})
VOLATILE_FIELDS = frozenset({"LastViewedDate", "LastReferencedDate", "LastLoginDate", "HtmlBody"})
SKIP_PREFIXES = ("UserPreferences", "UserPermissions")

_CRED = re.compile(r"(?m)^SALESFORCE_B2B_(USERNAME|PASSWORD|SECURITY_TOKEN)=(\S+)\s*$")


class ExportError(RuntimeError):
    """The export cannot proceed; the message never contains credentials."""


class SalesforceLike(Protocol):
    def describe_fields(self, sobject: str) -> list[dict[str, Any]]: ...
    def query_all(self, soql: str) -> list[dict[str, Any]]: ...


def parse_credentials(readme: str) -> dict[str, str]:
    """The three SALESFORCE_B2B_* values of the README; raises without echoing them."""
    creds = dict(_CRED.findall(readme))
    missing = {"USERNAME", "PASSWORD", "SECURITY_TOKEN"} - set(creds)
    if missing:
        raise ExportError(f"README no longer lists SALESFORCE_B2B_{sorted(missing)[0]}")
    return creds


def exported_fields(describe: list[dict[str, Any]]) -> list[str]:
    """Field names worth exporting, in describe order."""
    return [f["name"] for f in describe
            if f["type"] not in SKIP_TYPES and f["name"] not in VOLATILE_FIELDS
            and not f["name"].startswith(SKIP_PREFIXES)]


def clean(records: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Records without Salesforce `attributes`, sorted by Id (new dicts; input untouched)."""
    out = [{k: v for k, v in r.items() if k != "attributes"} for r in records]
    return sorted(out, key=lambda r: r.get("Id") or "")


def serialize(records: Any) -> bytes:
    """Deterministic JSON: sorted keys, two-space indent, UTF-8, trailing newline."""
    return (json.dumps(records, indent=2, sort_keys=True, ensure_ascii=False) + "\n").encode("utf-8")


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def build_manifest(files: dict[str, bytes], counts: dict[str, int], exported_at: str) -> dict[str, Any]:
    return {
        "dataset": DATASET,
        "source_url": SOURCE_URL,
        "licence": LICENCE,
        "licence_url": LICENCE_URL,
        "third_party_synthetic": True,
        "exported_at": exported_at,
        "counts": dict(sorted(counts.items())),
        "sha256": {name: sha256(data) for name, data in sorted(files.items())},
    }


def write_if_changed(path: Path, data: bytes) -> bool:
    """Writes data unless the file already holds exactly it; reports whether it wrote."""
    if path.exists() and path.read_bytes() == data:
        return False
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_bytes(data)
    tmp.replace(path)
    return True


def export(sf: SalesforceLike, out: Path, objects: tuple[str, ...] = OBJECTS, now: datetime | None = None) -> dict[str, Any]:
    """Exports every object to out/ and returns the manifest that is now on disk."""
    out.mkdir(parents=True, exist_ok=True)
    files: dict[str, bytes] = {}
    counts: dict[str, int] = {}
    for name in objects:
        fields = exported_fields(sf.describe_fields(name))
        if "Id" not in fields:
            raise ExportError(f"{name} has no Id field")
        records = clean(sf.query_all(f"SELECT {', '.join(fields)} FROM {name}"))
        files[f"{name}.json"] = serialize(records)
        counts[name] = len(records)
    for fname, data in files.items():
        write_if_changed(out / fname, data)
    return _settle_manifest(out / "manifest.json", files, counts, now or datetime.now(timezone.utc))


def _settle_manifest(path: Path, files: dict[str, bytes], counts: dict[str, int], now: datetime) -> dict[str, Any]:
    """Keeps the existing manifest when the content hashes are unchanged (idempotent re-run)."""
    fresh = build_manifest(files, counts, now.strftime("%Y-%m-%dT%H:%M:%SZ"))
    if path.exists():
        old = json.loads(path.read_text(encoding="utf-8"))
        if old.get("sha256") == fresh["sha256"] and old.get("counts") == fresh["counts"]:
            return old
    write_if_changed(path, serialize(fresh))
    return fresh


class _SimpleSalesforce:
    """Read-only adapter over simple_salesforce: describe and query only."""

    def __init__(self, creds: dict[str, str]) -> None:
        from simple_salesforce import Salesforce  # imported lazily: tests use a fake

        self._sf = Salesforce(username=creds["USERNAME"], password=creds["PASSWORD"],
                              security_token=creds["SECURITY_TOKEN"])

    def describe_fields(self, sobject: str) -> list[dict[str, Any]]:
        return getattr(self._sf, sobject).describe()["fields"]

    def query_all(self, soql: str) -> list[dict[str, Any]]:
        return self._sf.query_all(soql)["records"]


def connect() -> SalesforceLike:
    with urllib.request.urlopen(SOURCE_URL, timeout=30) as resp:  # noqa: S310 - fixed https URL
        readme = resp.read().decode("utf-8")
    try:
        return _SimpleSalesforce(parse_credentials(readme))
    except ExportError:
        raise
    except Exception as exc:  # never let a library error echo the login arguments
        raise ExportError(f"login to the CRMArena B2B org failed ({type(exc).__name__})") from None


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--out", type=Path, default=DEFAULT_OUT)
    args = ap.parse_args(argv)
    manifest = export(connect(), args.out)
    for name, n in manifest["counts"].items():
        print(f"{name:24s} {n:6d}")
    print(f"manifest: {args.out / 'manifest.json'} (exported_at {manifest['exported_at']})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
