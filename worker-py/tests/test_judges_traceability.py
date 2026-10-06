"""WP18 (HAR-116): every HAR-97 line acknowledged for HAR-116 is built and traced.

docs/traceability/wp18_har97_lines.txt is a snapshot of the 106 HAR-97 description lines that end in
'✅ acknowledged (Claude Code · HAR-116)' (line number, tab, exact text). Each one must be cited, with its exact
text, by a rubric (`har97_refs` or a criterion's `har97`) or an L5 row, and must have a row in wp18.md."""
from __future__ import annotations

from pathlib import Path

from judge_doubles import RUBRICS

from ghost_worker.judges.meta import L5_ROWS

DOCS = Path(__file__).resolve().parents[2] / "docs" / "traceability"
SNAPSHOT = DOCS / "wp18_har97_lines.txt"


def snapshot() -> dict[int, str]:
    lines = {}
    for row in SNAPSHOT.read_text(encoding="utf-8").splitlines():
        number, text = row.split("\t", 1)
        lines[int(number.removeprefix("L"))] = text
    return lines


def citations() -> dict[int, list[tuple[str, str]]]:
    """HAR-97 line -> [(where, quoted text)] over rubrics and L5 rows."""
    cited: dict[int, list[tuple[str, str]]] = {}
    for name, rubric in RUBRICS.items():
        for ref in rubric.har97_refs:
            cited.setdefault(ref.line, []).append((f"rubric {name}", ref.text))
        for c in rubric.criteria:
            if c.har97 is not None:
                cited.setdefault(c.har97.line, []).append((f"rubric {name} criterion {c.id}", c.har97.text))
    for row in L5_ROWS:
        cited.setdefault(row.line, []).append((f"L5 {row.dimension}", row.text))
    return cited


def test_snapshot_has_the_106_marked_lines() -> None:
    lines = snapshot()
    assert len(lines) == 106
    sections = [t for t in lines.values() if t.startswith("## ")]
    assert len([t for t in sections if t.startswith("## 5.")]) == 16
    assert len([t for t in sections if t.startswith("## 15.")]) == 12


def test_every_marked_line_is_built_with_its_exact_text() -> None:
    lines, cited = snapshot(), citations()
    assert set(cited) == set(lines), {"uncovered": sorted(set(lines) - set(cited)),
                                      "not in snapshot": sorted(set(cited) - set(lines))}
    for number, refs in cited.items():
        for where, text in refs:
            assert text == lines[number], (number, where, text)


def test_each_line_is_built_exactly_once() -> None:
    assert all(len(refs) == 1 for refs in citations().values())


def test_l2_dimension_lists_map_to_judges() -> None:
    """Both HAR-97 L2 lists (lines 603-614 and 2953-2964) name a judge; each L2 judge declares its dimension."""
    lines, cited = snapshot(), citations()
    l2 = [n for n in lines if 603 <= n <= 614 or 2953 <= n <= 2964]
    assert len(l2) == 24 and all(cited[n][0][0].startswith("rubric ") for n in l2)
    with_dimension = {n for n, r in RUBRICS.items() if r.l2_dimension}
    assert with_dimension == {cited[n][0][0].removeprefix("rubric ") for n in l2}


def test_wp18_table_lists_every_line() -> None:
    doc = (DOCS / "wp18.md").read_text(encoding="utf-8")
    for number, text in snapshot().items():
        assert f"| L{number} | {text} |" in doc, number
