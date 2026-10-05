"""Rewrite .github/workflows/ci.yml for the public CI mirror (harneet2512/gtm-work-ci).

The mirror carries code only: no Markdown, no docs/ and no verbatim ticket text. This rewrite:
- runs CI on a push to any branch, because the mirror receives snapshots, not pull requests;
- drops the linear-trailer job (commit trailers and traceability docs are enforced in the private repo);
- skips the tests that read the stripped documents, as listed in scripts/ci/mirror_skips.json.

Those skipped tests must still pass locally before a merge.
Usage: python mirror_workflow.py <ci.yml path> <skips.json path>  (rewrites the file in place)
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

TRIGGER = "on:\n  push:\n    branches: ['**']\n  workflow_dispatch:\n"


GO_SHARDS = 6  # parallel go jobs; the public mirror allows 20 concurrent jobs

# The go test command of ci.yml, whatever flags (-race, -timeout 20m) sit before -coverprofile: group 1 is the command up
# to and including -coverprofile, group 2 an optional -skip already added, then the package pattern.
GO_TEST = re.compile(r"(go test [^\n]*?-coverprofile=coverage\.out)( -skip '[^']*')? \./\.\.\.")


def rewrite(text: str, skips: dict[str, list[str]], go_shards: int = GO_SHARDS) -> str:
    text = re.sub(r"(?ms)^on:\n.*?(?=^\S)", TRIGGER, text, count=1)
    text = re.sub(r"(?ms)^  linear-trailer:\n.*?(?=^  \S|\Z)", "", text)
    go_skip = "|".join(skips.get("go", []))
    if go_skip:
        text = GO_TEST.sub(lambda m: f"{m.group(1)} -skip '^({go_skip})$' ./...", text, count=1)
    if go_shards > 1:
        # Each shard is its own job with its own Postgres and Neo4j services; packages are dealt round-robin
        # from `go list ./...`, so every package runs in exactly one shard.
        shards = ", ".join(str(i) for i in range(go_shards))
        text = text.replace("  go:\n    runs-on:",
                            f"  go:\n    strategy:\n      fail-fast: false\n      matrix:\n        shard: [{shards}]\n    runs-on:", 1)
        pick = f"$(go list ./... | awk -v n={go_shards} -v i=${{{{ matrix.shard }}}} '(NR-1) % n == i')"
        text = GO_TEST.sub(lambda m: f"{m.group(1)}{m.group(2) or ''} {pick}", text, count=1)
    deselect = " ".join([f"--ignore={f}" for f in skips.get("python_ignore", [])]
                        + [f"--deselect {t}" for t in skips.get("python", [])])
    if deselect:
        text = text.replace("python -m pytest --cov=ghost_worker --cov-report=term-missing",
                            f"python -m pytest --cov=ghost_worker --cov-report=term-missing {deselect}")
    return text


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    ci, skips_path = Path(argv[1]), Path(argv[2])
    skips = json.loads(skips_path.read_text(encoding="utf-8")) if skips_path.exists() else {}
    ci.write_text(rewrite(ci.read_text(encoding="utf-8"), skips), encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
