"""WP20 (HAR-118): every HAR-97 line acknowledged for HAR-118 has one traceability row with real code and tests."""
from __future__ import annotations

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DOCS = ROOT / "docs" / "traceability"
SNAPSHOT = (DOCS / "wp20_har97_lines.txt").read_text(encoding="utf-8").splitlines()
TABLE = (DOCS / "wp20.md").read_text(encoding="utf-8")
MARK = "— ✅ acknowledged (Claude Code · HAR-118)"


def rows() -> dict[str, list[str]]:
    out: dict[str, list[str]] = {}
    for line in TABLE.splitlines():
        cells = [c.strip() for c in line.strip().strip("|").split("|")]
        if len(cells) == 5 and cells[0].isdigit():
            out[cells[1]] = cells
    return out


def test_snapshot_holds_the_nine_har118_lines() -> None:
    assert len(SNAPSHOT) == 9
    assert all(line.endswith(MARK) for line in SNAPSHOT)
    assert len(set(SNAPSHOT)) == 9


def test_every_snapshot_line_has_exactly_one_row() -> None:
    table = rows()
    assert set(table) == set(SNAPSHOT)
    assert [int(table[line][0]) for line in SNAPSHOT] == list(range(1, 10))


def test_rows_point_at_existing_code_and_tests_with_a_status() -> None:
    for line, cells in rows().items():
        _, _, code, tests, status = cells
        for column in (code, tests):
            paths = re.findall(r"`([\w./-]+\.(?:go|py))`", column)
            assert paths, f"no path in {line!r}"
            for p in paths:
                assert (ROOT / p).is_file(), f"{p} named for {line!r} does not exist"
        assert all("test" in Path(p).name for p in re.findall(r"`([\w./-]+\.(?:go|py))`", tests))
        assert status.startswith(("✅", "⚠️")), status
