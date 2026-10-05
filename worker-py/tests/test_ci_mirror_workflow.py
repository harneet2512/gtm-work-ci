"""The public CI mirror's workflow rewrite (scripts/ci/mirror_workflow.py).

The mirror receives code-only snapshots (no Markdown, no docs/), so its CI must: run on a push to any
branch, drop the PR-only linear-trailer job, and skip exactly the tests that read stripped documents.
The tests use a self-contained sample workflow: inside the mirror the repo's own ci.yml is already
rewritten, so it cannot serve as the input.
"""
from __future__ import annotations

import importlib.util
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

SAMPLE = """name: ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - run: go test -p 1 -race -coverprofile=coverage.out ./...
  python:
    runs-on: ubuntu-latest
    steps:
      - run: python -m pytest --cov=ghost_worker --cov-report=term-missing
  linear-trailer:
    if: github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    steps:
      - run: echo check trailers
  web:
    runs-on: ubuntu-latest
    steps:
      - run: npm run test:e2e
"""


def _module():
    spec = importlib.util.spec_from_file_location("mirror_workflow", ROOT / "scripts" / "ci" / "mirror_workflow.py")
    assert spec is not None and spec.loader is not None
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def test_the_mirror_runs_on_any_branch_push_and_drops_the_trailer_job() -> None:
    out = _module().rewrite(SAMPLE, {})
    assert "branches: ['**']" in out
    assert "pull_request" not in out.split("jobs:")[0]
    assert "linear-trailer" not in out
    for job in ("  go:", "  python:", "  web:"):
        assert job in out


def test_doc_dependent_tests_are_skipped_only_by_explicit_list() -> None:
    skips = {"go": ["TestA", "TestB"], "python_ignore": ["tests/test_x.py"], "python": ["tests/test_y.py::test_z"]}
    out = _module().rewrite(SAMPLE, skips)
    assert "-skip '^(TestA|TestB)$'" in out
    assert "--ignore=tests/test_x.py" in out
    assert "--deselect tests/test_y.py::test_z" in out


def test_no_skips_leaves_the_test_commands_untouched() -> None:
    out = _module().rewrite(SAMPLE, {}, go_shards=1)
    assert "go test -p 1 -race -coverprofile=coverage.out ./...\n" in out
    assert "python -m pytest --cov=ghost_worker --cov-report=term-missing\n" in out


def test_the_go_job_is_split_into_parallel_shards_covering_every_package() -> None:
    out = _module().rewrite(SAMPLE, {"go": ["TestA"]}, go_shards=6)
    go_job = out.split("  go:\n", 1)[1].split("\n  python:", 1)[0]
    assert "fail-fast: false" in go_job
    assert "shard: [0, 1, 2, 3, 4, 5]" in go_job
    # Every package lands in exactly one shard: round-robin over `go list ./...` by line number.
    assert "$(go list ./... | awk -v n=6 -v i=${{ matrix.shard }} '(NR-1) % n == i')" in go_job
    assert "-skip '^(TestA)$'" in go_job
    assert "./...\n" not in go_job.split("go test", 1)[1].split("\n", 1)[0] + "\n"


def test_the_committed_skip_list_names_only_existing_test_files() -> None:
    import json

    skips = json.loads((ROOT / "scripts" / "ci" / "mirror_skips.json").read_text(encoding="utf-8"))
    for entry in skips.get("python_ignore", []) + [s.split("::")[0] for s in skips.get("python", [])]:
        assert (ROOT / "worker-py" / entry).exists(), entry
