"""HAR-114 gold v2, part C: the command that runs the WP18 judge benchmark over the CRMArena gold in REPLAY mode only.

It cannot make a live call: it has no --mode, it points at its own cassette folder, and recording is the lead's job. On a
tree without the WP18 judges it exits 2 with a message; with them (after WP18 merges) its --dry-run builds every request."""
from __future__ import annotations

import argparse
import sys

import pytest

from crmarena_gold_lib import CR_CASES, ROOT

sys.path.insert(0, str(ROOT / "bench" / "evals"))
import run_crmarena_judges as cmd  # noqa: E402

HAS_JUDGES = (ROOT / "bench" / "evals" / "run_judges.py").exists()


def test_the_command_has_no_way_to_go_live() -> None:
    with pytest.raises(SystemExit):
        cmd.parse_args(["--mode", "live"])
    args = cmd.parse_args([])
    assert not hasattr(args, "mode") and args.selection == "gold" and args.trials == 1
    text = (ROOT / "bench" / "evals" / "run_crmarena_judges.py").read_text(encoding="utf-8")
    assert 'mode="replay"' in text and '"--mode"' not in text and "mode=\"live\"" not in text and "mode=\"record\"" not in text


def test_it_reads_the_crmarena_cases_and_its_own_cassettes() -> None:
    assert cmd.CASES_DIR == ROOT / "fixtures" / "evals" / "crmarena" / "cases"
    assert cmd.CASSETTES.name == "judges-crmarena" and cmd.CASSETTES.parent.name == "cassettes"
    assert "NOT human labels" in cmd.PROVENANCE and len(list(cmd.CASES_DIR.glob("*/*.json"))) == len(CR_CASES)


@pytest.mark.skipif(HAS_JUDGES, reason="the WP18 judges are present")
def test_without_the_wp18_judges_it_exits_two_with_a_message(capsys: pytest.CaptureFixture[str]) -> None:
    assert cmd.main(["--dry-run"]) == cmd.NO_JUDGES
    assert "WP18" in capsys.readouterr().err


@pytest.mark.skipif(not HAS_JUDGES, reason="needs the WP18 judges (bench/evals/run_judges.py)")
def test_dry_run_builds_a_request_for_every_semantic_gold_judgment(capsys: pytest.CaptureFixture[str]) -> None:
    assert cmd.main(["--dry-run"]) == 0
    out = capsys.readouterr().out
    assert f"'cases': {len(CR_CASES)}" in out and "'requests':" in out


def test_parser_accepts_the_documented_options() -> None:
    args = cmd.parse_args(["--dry-run", "--selection", "routed", "--case", "cr_d1", "--trials", "2", "--model", "m"])
    assert isinstance(args, argparse.Namespace) and (args.dry_run, args.selection, args.case, args.trials, args.model) == (True, "routed", ["cr_d1"], 2, ["m"])
