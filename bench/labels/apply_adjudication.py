"""Apply bench/labels/adjudication-2026-10-02.json to the legacy eval gold (HAR-114, gold v2).

    python bench/labels/apply_adjudication.py [--check]

Three kinds of edit, all listed in the record and nothing else:
  applied        a judgment (verdict, label, diagnostics, blocking, rationale), plus the case's should_pass,
                 slice_tags.candidate and a `gold_revision` note (marked contested when the record says so);
  case_edits     wording that the flipped verdict made stale (title, notes, best-action why, a debate note);
  context_edits  a fact the case context was missing (rejected corrections can still add context).
The edit is a line patch, not a JSON re-dump, so the hand-formatted fixtures keep their layout. Idempotent: a case that
already carries the revision (or the context edit) is skipped. With --check nothing is written and the exit code says
whether the fixtures already match the record. worker-py/tests/test_gold_adjudication.py reverses these edits and compares
the result with the base hash in the record, so any other change to the gold fails CI.

The labels are model labels (two LLM labellers and a GPT-5.6 tie-break), not human labels.
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RECORD = ROOT / "bench" / "labels" / "adjudication-2026-10-02.json"
CASES = ROOT / "fixtures" / "evals" / "cases"
REVISION = "gold-v2"
JUDGMENT_KEYS = ("verdict", "label", "diagnostics", "blocking", "rationale")


def dumps(value: object) -> str:
    return json.dumps(value, ensure_ascii=False)


def patch_judgment(lines: list[str], eval_type: str, new: dict) -> list[str]:
    """New list of lines with the one judgment `eval_type` rewritten (judgment keys sit on their own lines)."""
    marker = f'"eval_type": "{eval_type}"'
    start = next((i for i, ln in enumerate(lines) if marker in ln), None)
    if start is None:
        raise ValueError(f"judgment {eval_type} not found")
    out = list(lines)
    i = start
    while not out[i].startswith("    }"):
        for key in JUDGMENT_KEYS:
            if out[i].lstrip().startswith(f'"{key}":'):
                indent = out[i][: len(out[i]) - len(out[i].lstrip())]
                comma = "," if out[i].rstrip().endswith(",") else ""
                out[i] = f"{indent}\"{key}\": {dumps(new[key])}{comma}"
        i += 1
    return out


def patch_line(lines: list[str], prefix: str, value: object, after: int = 0) -> list[str]:
    """Rewrite the first line at or after `after` that starts with `prefix` (e.g. '"should_pass":')."""
    out = list(lines)
    for i in range(after, len(out)):
        if out[i].lstrip().startswith(prefix):
            indent = out[i][: len(out[i]) - len(out[i].lstrip())]
            comma = "," if out[i].rstrip().endswith(",") else ""
            out[i] = f"{indent}{prefix} {dumps(value)}{comma}"
            return out
    raise ValueError(f"line {prefix} not found")


def add_revision(lines: list[str], note: dict) -> list[str]:
    i = next(i for i, ln in enumerate(lines) if ln.startswith('  "notes":'))
    return [*lines[:i], f'  "gold_revision": [{dumps(note)}],', *lines[i:]]


def replace_once(text: str, before: str, after: str) -> str:
    old, new = dumps(before), dumps(after)
    if text.count(old) != 1:
        raise ValueError(f"edit target not unique: {before[:60]!r}")
    return text.replace(old, new)


def add_timeline_notes(text: str, notes: str) -> str:
    """Insert `notes` after timeline_facts.stated_timing (the case files keep one key per line)."""
    lines = text.split("\n")
    start = next(i for i, ln in enumerate(lines) if ln.lstrip().startswith('"timeline_facts"'))
    i = next(i for i in range(start, len(lines)) if lines[i].lstrip().startswith('"stated_timing"'))
    indent = lines[i][: len(lines[i]) - len(lines[i].lstrip())]
    lines[i] = lines[i].rstrip() + ","
    lines.insert(i + 1, f'{indent}"notes": {dumps(notes)}')
    return "\n".join(lines)


def apply_judgments(path: Path, items: list[dict], edits: list[dict], source: str) -> tuple[str, str]:
    text = path.read_text(encoding="utf-8")
    if '"gold_revision"' in text:
        return text, ""
    case = json.loads(text)
    lines = text.split("\n")
    verdicts = {e["eval_type"]: e["verdict"] for e in case["expected"]}
    changes = []
    for item in items:
        lines = patch_judgment(lines, item["eval_type"], item["applied"])
        change = {"eval_type": item["eval_type"], "from": item["gold_before"]["verdict"], "to": item["applied"]["verdict"]}
        changes.append({**change, **({"contested": True} if item.get("contested") else {})})
        verdicts[item["eval_type"]] = item["applied"]["verdict"]
    should_pass = "fail" not in verdicts.values()
    lines = patch_line(lines, '"should_pass":', should_pass)
    tags = next(i for i, ln in enumerate(lines) if ln.lstrip().startswith('"slice_tags"'))
    lines = patch_line(lines, '"candidate":', "good" if should_pass else "flawed", after=tags)
    lines = add_revision(lines, {"revision": REVISION, "source": source, "label_kind": "model labels, not human labels", "changes": changes})
    out = "\n".join(lines)
    for edit in edits:
        out = replace_once(out, edit["before"], edit["after"])
    flipped = f"should_pass {case['should_pass']} -> {should_pass}" if should_pass != case["should_pass"] else "should_pass unchanged"
    return out, f"{case['id']}: {len(changes)} change(s), {len(edits)} wording edit(s), {flipped}"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args(argv)
    record = json.loads(RECORD.read_text(encoding="utf-8"))
    by_case: dict[str, list[dict]] = {}
    for item in record["applied"]:
        by_case.setdefault(item["case"], []).append(item)
    pending = 0
    for cid, items in sorted(by_case.items()):
        path = next(CASES.glob(f"*/{cid}.json"))
        text, summary = apply_judgments(path, items, record.get("case_edits", {}).get(cid, []), record["source"])
        if summary:
            pending += 1
            print(summary)
            if not args.check:
                path.write_text(text, encoding="utf-8", newline="\n")
    for edit in record.get("context_edits", []):
        path = next(CASES.glob(f"*/{edit['case']}.json"))
        text = path.read_text(encoding="utf-8")
        if edit["after"] in text:
            continue
        pending += 1
        print(f"{edit['case']}: context edit {edit['path']}")
        if not args.check:
            path.write_text(add_timeline_notes(text, edit["after"]), encoding="utf-8", newline="\n")
    return 1 if args.check and pending else 0


if __name__ == "__main__":
    sys.exit(main())
