"""Fetch the CRMArena-Pro B2B snapshot into data/crmarena_b2b and prove it is the one the demo was mined on.

    python scripts/codespace/fetch_crmarena.py [--out data/crmarena_b2b]

The data is a public Salesforce AI Research dataset (CC BY-NC 4.0) and is git-ignored: it is never committed.
bench/data/crmarena_export.py reads the org with the credentials Salesforce publishes in the CRMArena README
(read-only: describe and SELECT). The export is idempotent: when the manifest on disk already lists the same file
hashes it is kept as it is. This script therefore seeds the PINNED manifest (metadata only: counts and a SHA-256
per file, committed at bench/data/crmarena_b2b.manifest.pinned.json) before it exports, and then checks the
export hash the frozen deal split and the synthetic layer are tied to (b553e9a3...). A fresh export of an
unchanged org reproduces it byte for byte. The org is shared and publicly writable, so when it has changed the
script says so and exits 2; use the one-time `gh codespace cp` upload then (docs/demo/codespace.md).

Exit codes: 0 verified (already present or fetched), 2 hash mismatch, 1 any other failure.
"""
from __future__ import annotations

import argparse
import hashlib
import sys
from pathlib import Path
from typing import Callable

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench" / "data"))

import crmarena_export  # noqa: E402

EXPECTED_EXPORT_SHA256 = "b553e9a347986d64c03f9a3cc12d9882282cfa7da403e8970ee9e4ae25bb9389"
PINNED_MANIFEST = ROOT / "bench" / "data" / "crmarena_b2b.manifest.pinned.json"
DEFAULT_OUT = ROOT / "data" / "crmarena_b2b"


class HashMismatch(RuntimeError):
    """The data on disk is not the snapshot the demo was mined on."""


def export_hash(directory: Path) -> str:
    """sha256 over the sorted (name, sha256) of every *.json: the same definition as bench.synthetic.base.export_hash."""
    digest = hashlib.sha256()
    for path in sorted(directory.glob("*.json")):
        digest.update(path.name.encode("utf-8"))
        digest.update(hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def verify(directory: Path, expected: str = EXPECTED_EXPORT_SHA256) -> str:
    """The export hash of directory; raises HashMismatch (naming both hashes) when it is not the expected one."""
    got = export_hash(directory)
    if got != expected:
        raise HashMismatch(f"export hash {got} is not the pinned {expected}")
    return got


def is_present(directory: Path, expected: str = EXPECTED_EXPORT_SHA256) -> bool:
    """True when directory already holds the verified snapshot (nothing to fetch)."""
    if not (directory / "Opportunity.json").exists():
        return False
    try:
        verify(directory, expected)
    except HashMismatch:
        return False
    return True


def seed_pinned_manifest(directory: Path, pinned: Path = PINNED_MANIFEST) -> None:
    """Write the pinned manifest into directory so an export of unchanged data keeps it (export is idempotent)."""
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "manifest.json").write_bytes(pinned.read_bytes())


def fetch(out: Path, connect: Callable[[], object] = crmarena_export.connect,
          expected: str = EXPECTED_EXPORT_SHA256, pinned: Path = PINNED_MANIFEST) -> str:
    """Export the org into out and verify the hash. Idempotent: a verified directory is not touched."""
    if is_present(out, expected):
        return expected
    seed_pinned_manifest(out, pinned)
    crmarena_export.export(connect(), out)
    return verify(out, expected)


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--out", type=Path, default=DEFAULT_OUT)
    args = ap.parse_args(argv)
    if is_present(args.out, EXPECTED_EXPORT_SHA256):
        print(f"crmarena_b2b already present and verified (export hash {EXPECTED_EXPORT_SHA256[:12]}...)")
        return 0
    try:
        fetch(args.out)
    except HashMismatch as exc:
        print(f"MISMATCH: {exc}. The public org changed since the demo was mined; upload the snapshot instead "
              f"(docs/demo/codespace.md, 'If the fetch does not verify').", file=sys.stderr)
        return 2
    except crmarena_export.ExportError as exc:
        print(f"export failed: {exc}", file=sys.stderr)
        return 1
    print(f"crmarena_b2b fetched into {args.out}; export hash {EXPECTED_EXPORT_SHA256[:12]}... verified")
    return 0


if __name__ == "__main__":
    sys.exit(main())
