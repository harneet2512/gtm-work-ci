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


def rewrite(text: str, skips: dict[str, list[str]]) -> str:
    text = re.sub(r"(?ms)^on:\n.*?(?=^\S)", TRIGGER, text, count=1)
    text = re.sub(r"(?ms)^  linear-trailer:\n.*?(?=^  \S|\Z)", "", text)
    go_skip = "|".join(skips.get("go", []))
    if go_skip:
        text = text.replace("go test -p 1 -race -coverprofile=coverage.out ./...",
                            f"go test -p 1 -race -coverprofile=coverage.out -skip '^({go_skip})$' ./...")
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
