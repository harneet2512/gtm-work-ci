"""Manifest and verification of the CRMArena extraction cassettes (HAR-104 / HAR-130).

    python bench/data/crmarena_cassettes.py manifest <cassette-dir> <manifest.json>   # writes the manifest
    python bench/data/crmarena_cassettes.py verify   <cassette-dir> <manifest.json>   # exit 1 when it does not match

The cassettes (about 99 MB) hold full prompts of CC BY-NC CRMArena text and stay out of git; the committed manifest pins them by
file count, total size and an aggregate SHA-256 (over sorted '<file name>:<sha256 of file bytes>' lines). See docs/data/crmarena-b2b.md
for where they are stored.
"""
from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path

ATTRIBUTION = "Salesforce AI Research, CRMArena-Pro (https://github.com/SalesforceAIResearch/CRMArena), CC BY-NC 4.0"


def fingerprint(directory: Path) -> dict:
    names = sorted(p.name for p in directory.glob("*.json"))
    aggregate, total = hashlib.sha256(), 0
    for name in names:
        data = (directory / name).read_bytes()
        total += len(data)
        aggregate.update(name.encode() + b":" + hashlib.sha256(data).hexdigest().encode() + b"\n")
    return {"files": len(names), "total_bytes": total, "aggregate_sha256": aggregate.hexdigest()}


def make_manifest(directory: Path) -> dict:
    return {"what": "Cassettes recorded by the worker in GHOST_LLM_MODE=record during the CRMArena-Pro extraction run (HAR-104 / HAR-130)",
            "model": "openrouter/deepseek/deepseek-v4-flash", "extractor_version": "extract-v4", **fingerprint(directory),
            "aggregate_definition": "sha256 over sorted lines '<file name>:<sha256 of file bytes>\\n'",
            "not_committed_because": "about 99 MB, over the 20 MB limit; they hold full prompts (CRMArena email text, CC BY-NC 4.0, third-party synthetic data)",
            "attribution": ATTRIBUTION}


def verify(directory: Path, manifest: dict) -> list[str]:
    """Differences between the directory and the manifest (empty when they match)."""
    actual = fingerprint(directory)
    return [f"{k}: manifest {manifest.get(k)!r}, directory {actual[k]!r}" for k in actual if manifest.get(k) != actual[k]]


def main(argv: list[str]) -> int:
    if len(argv) != 3 or argv[0] not in ("manifest", "verify"):
        print(__doc__)
        return 2
    directory, manifest_path = Path(argv[1]), Path(argv[2])
    if argv[0] == "manifest":
        manifest_path.write_text(json.dumps(make_manifest(directory), indent=1) + "\n", encoding="utf-8")
        return 0
    problems = verify(directory, json.loads(manifest_path.read_text(encoding="utf-8")))
    print("cassettes match the manifest" if not problems else "\n".join(problems))
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
