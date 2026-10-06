"""Known extraction misses (HAR-124): prompts the recorded CRMArena cassette set has no answer for, and that the demo-case mining
answered with no claims. Replaying them the same way keeps the frozen history identical to the reviewed one; any OTHER miss stays an error.

An entry is identified by its loose key (system prompt, activity header, email text): the same identity the approximate tier uses, so a
re-import that only reorders the known-people block still names the same entry. The exact key (the miss file's stem) is kept for audit.

    python bench/data/known_misses.py --misses D:\\ghost-demo\\data\\crmarena_extraction\\misses --out bench/data/known_extraction_misses.json
"""
from __future__ import annotations

import argparse
import hashlib
import json
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "worker-py"))
sys.path.insert(0, str(Path(__file__).resolve().parent))

VERSION = "ghost-known-extraction-misses.v1"
DEFAULT_MANIFEST = Path(__file__).resolve().parent / "known_extraction_misses.json"


def _context() -> tuple[str, str, str]:
    from ghost_worker.extract.prompt import build_system_prompt
    from ghost_worker.extract.schema import OUTPUT_SCHEMA, OUTPUT_SCHEMA_NAME
    from ghost_worker.llm.fake_provider import schema_sha256

    return build_system_prompt(), OUTPUT_SCHEMA_NAME, schema_sha256(OUTPUT_SCHEMA)


def prompt_fingerprint() -> str:
    """Digest of the system prompt and schema the keys were computed under; a manifest from another prompt is stale."""
    system, name, sha = _context()
    return hashlib.sha256(json.dumps([system, name, sha]).encode("utf-8")).hexdigest()


def entries_from_directory(directory: Path) -> list[dict[str, str]]:
    """One entry per <exact key>.txt miss dump. A dump whose name is not the key of its own text was recorded under another prompt: refused."""
    import crmarena_replay_worker as rw

    system, name, sha = _context()
    out = []
    for path in sorted(directory.glob("*.txt")):
        user = path.read_text(encoding="utf-8")
        exact = rw.replay_key(system, user, name, sha)
        if exact != path.stem:
            raise ValueError(f"{path.name}: the dump is not a miss of the current prompt (its key is {exact[:12]}); regenerate the misses")
        out.append({"exact_key": exact, "loose_key": rw.loose_key(system, user, name, sha), "activity": rw.mask_nonce(user).split("\n", 1)[0]})
    return out


def build_manifest(directory: Path) -> dict[str, Any]:
    entries = entries_from_directory(directory)
    return {"version": VERSION, "prompt_sha256": prompt_fingerprint(), "count": len(entries), "misses": entries}


def load_loose_keys(path: Path) -> dict[str, str]:
    """Loose key -> exact key, from a miss directory (derived now) or a checked-in manifest (checked against the current prompt)."""
    if path.is_dir():
        return {e["loose_key"]: e["exact_key"] for e in entries_from_directory(path)}
    doc = json.loads(path.read_text(encoding="utf-8"))
    if doc.get("version") != VERSION:
        raise ValueError(f"{path}: not a {VERSION} manifest")
    if doc.get("prompt_sha256") != prompt_fingerprint():
        raise ValueError(f"{path}: written under another extraction prompt; rebuild it from the misses directory")
    if doc.get("count") != len(doc.get("misses", [])):
        raise ValueError(f"{path}: count does not match its entries")
    return {e["loose_key"]: e["exact_key"] for e in doc["misses"]}


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--misses", type=Path, required=True)
    ap.add_argument("--out", type=Path, default=DEFAULT_MANIFEST)
    args = ap.parse_args()
    manifest = build_manifest(args.misses)
    args.out.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(f"{manifest['count']} known misses written to {args.out}")


if __name__ == "__main__":
    main()
