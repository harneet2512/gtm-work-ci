"""CRMArena gold generator (HAR-114 gold v2, part B).

    python bench/evals/crmarena_gold/cli.py sheets --out SHEETS.txt   # blind labelling sheets (no intent, no pass 1)
    python bench/evals/crmarena_gold/cli.py build                     # cases + manifest under fixtures/evals/crmarena/
    python bench/evals/crmarena_gold/cli.py agree                     # self-agreement report of the two passes
    python bench/evals/crmarena_gold/cli.py verify                    # rebuild in memory and compare with the committed files
    python bench/evals/crmarena_gold/cli.py needed                    # bench/labels/crmarena/labels_needed.json from the committed cases

`build` and `verify` need the git-ignored snapshot (bench/data/crmarena_export.py). The committed cases are the output; the
generator, the specs, the two label passes and a manifest hash make them reproducible and checkable.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import random
import sys
from pathlib import Path

if __package__ in (None, ""):  # run as a script: python bench/evals/crmarena_gold/<module>.py
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
    __package__ = "crmarena_gold"

from . import assemble
from . import build
from . import labels as L
from . import render
from .all_specs import SPECS
from .source import Snapshot

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / "fixtures" / "evals" / "crmarena"
REPORT = ROOT / "bench" / "reports" / "crmarena-gold-agreement-2026-10-02.json"
NL = chr(10)
SNAPSHOT_FILES = ("Account", "Contact", "Contract", "EmailMessage", "Opportunity", "Quote", "Task", "User")


def dumps(case: dict) -> str:
    return json.dumps(case, indent=1, ensure_ascii=False) + "\n"


def cases_from_snapshot() -> tuple[dict[str, str], Snapshot]:
    snap = Snapshot()
    p1, p2, res = L.load_pass("pass_1"), L.load_pass("pass_2"), L.load_resolutions()
    out = {}
    for spec in SPECS:
        skeleton = build.build_skeleton(snap, spec)
        case = assemble.finalize(skeleton, spec, p1, p2, res)
        out[f"cases/{case['case_type']}/{case['id']}.json"] = dumps(case)
    return out, snap


def evidence(files: dict[str, str], snap: Snapshot) -> dict[str, dict[str, dict[str, str]]]:
    """Per case and activity: SHA-256 of the committed excerpt and of the full source body, so CI can check the excerpt
    against the manifest and a local run can check the body against the snapshot."""
    out: dict[str, dict[str, dict[str, str]]] = {}
    for text in files.values():
        case = json.loads(text)
        deal = case["based_on"]["deal_id"]
        acts = [case["context"]["trigger"], *case["context"]["supporting_activities"]]
        out[case["id"]] = {a["event_file"]: {
            "excerpt_sha256": hashlib.sha256(a["text"].encode("utf-8")).hexdigest(),
            "body_sha256": hashlib.sha256(snap.event(deal, a["event_file"].rsplit(":", 1)[1]).body.encode("utf-8")).hexdigest()} for a in acts}
    return out


def manifest(files: dict[str, str], snap: Snapshot) -> str:
    entries = {path: hashlib.sha256(text.encode("utf-8")).hexdigest() for path, text in sorted(files.items())}
    combined = hashlib.sha256("".join(f"{k}:{v}\n" for k, v in entries.items()).encode("utf-8")).hexdigest()
    body = {"generator": build.GENERATOR, "cases": len(entries), "cases_sha256": combined,
            "snapshot_manifest_sha256": snap.manifest_sha256,
            "snapshot_file_sha256": {k: snap.file_hashes[f"{k}.json"] for k in SNAPSHOT_FILES},
            "frozen_split": "bench/data/deal_split.json (cutoff 2023-11-01)",
            "evidence": evidence(files, snap),
            "licence": "CC BY-NC 4.0 (non-commercial use only); Salesforce AI Research, CRMArena-Pro", "files": entries}
    return json.dumps(body, indent=1, ensure_ascii=False) + "\n"


def neutral_plan(seed: int = 20261003) -> list[dict]:
    """Blind-sheet plan: random sheet ids, shuffled case order, shuffled eval order (ids no longer name the flaw)."""
    rng = random.Random(seed)
    order = list(SPECS)
    rng.shuffle(order)
    numbers = rng.sample(range(0x1000, 0xFFFF), len(order))
    plan = []
    for spec, number in zip(order, numbers):
        evals = list(spec["evals"])
        rng.shuffle(evals)
        plan.append({"sheet": f"S{number:04x}", "case": spec["id"], "evals": evals})
    return plan


def cmd_sheets(args: argparse.Namespace) -> int:
    snap = Snapshot()
    by_id = {s["id"]: s for s in SPECS}
    plan = neutral_plan()
    sheets = [render.sheet(build.build_skeleton(snap, by_id[e["case"]]), e["evals"], e["sheet"]) + "\n" for e in plan]
    Path(args.out).write_text("\n\n" + ("=" * 100 + "\n").join(sheets), encoding="utf-8")
    (ROOT / "bench" / "labels" / "crmarena" / "pass_2_map.json").write_text(
        json.dumps({"note": "neutral sheet id -> case; pass 2 is keyed by sheet id", "sheets": plan}, indent=1) + NL, encoding="utf-8", newline=NL)
    print(f"{len(plan)} blind sheets -> {args.out}")
    return 0


def cmd_build(_: argparse.Namespace) -> int:
    files, snap = cases_from_snapshot()
    for rel, text in files.items():
        path = OUT / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8", newline="\n")
    (OUT / "manifest.json").write_text(manifest(files, snap), encoding="utf-8", newline="\n")
    print(f"{len(files)} cases -> {OUT}")
    return 0


def cmd_verify(_: argparse.Namespace) -> int:
    files, snap = cases_from_snapshot()
    stale = [rel for rel, text in files.items() if not (OUT / rel).exists() or (OUT / rel).read_text(encoding="utf-8") != text]
    same_manifest = (OUT / "manifest.json").read_text(encoding="utf-8") == manifest(files, snap)
    print("committed cases match the snapshot rebuild" if not stale and same_manifest else f"MISMATCH: {stale} manifest_ok={same_manifest}")
    return 0 if not stale and same_manifest else 1


def cmd_agree(_: argparse.Namespace) -> int:
    p1, p2, res = L.load_pass("pass_1"), L.load_pass("pass_2"), L.load_resolutions()
    diffs = L.disagreements(p1, p2)
    cluster_of = {s["id"]: s["deal"] for s in SPECS}
    contexts = {(s["deal"], s["trigger"], s.get("now")) for s in SPECS}
    by_type: dict[str, dict] = {}
    for spec in SPECS:
        row = by_type.setdefault(spec["type"], {"cases": 0, "judgments": 0, "exact_agree": 0})
        row["cases"] += 1
        for e in spec["evals"]:
            row["judgments"] += 1
            row["exact_agree"] += not L.differs(p1[spec["id"]][e], p2[spec["id"]][e])
    rows = L.per_cluster(p1, p2, cluster_of)
    report = {"label_kind": "model labels, single author; consistency of one labeller across two passes, NOT inter-rater and NOT human agreement",
              "passes": {"pass_1": "written from the cases while designing them",
                         "pass_2": "written from sheets with neutral random ids, shuffled case and eval order and no design intent; the same author "
                                   "(one model), minutes later, in the same session: NOT blind independent labelling"},
              "kappa_caveat": "Pass 1 was revised in review before pass 2 was redone (4 semantic changes and 2 replaced cases), so the kappa "
                              "says little about the labelling itself; it mostly shows one author applying one rubric twice.",
              "caveat": "55 cases come from %d deals and %d distinct contexts, so the %d judgments are not independent; read the "
                        "cluster-bootstrap interval, not the pooled rate" % (len(rows), len(contexts), sum(r["judgments"] for r in rows.values())),
              **L.agreement(p1, p2), "fail_kappa_between_passes": L.kappa(p1, p2),
              "deal_clusters": len(rows), "distinct_contexts": len(contexts),
              "cluster_bootstrap_95ci": L.cluster_bootstrap(p1, p2, cluster_of), "per_deal": rows,
              "disagreements": len(diffs), "resolved": sum(1 for c, e, _, _ in diffs if c in res and e in res[c]), "by_case_type": by_type,
              "disagreement_list": [{"case": c, "eval_type": e, "pass_1": a[:3], "pass_2": b[:3]} for c, e, a, b in diffs]}
    REPORT.write_text(json.dumps(report, indent=1, ensure_ascii=False) + NL, encoding="utf-8", newline=NL)
    print(json.dumps({k: v for k, v in report.items() if k not in ("disagreement_list", "by_case_type", "per_deal")}, indent=1))
    return 0


ROLE_EVALS = ("champion_continuity", "stakeholder_selection", "stakeholder_coverage", "champion_strength", "economic_buyer_coverage")


def cmd_needed(_: argparse.Namespace) -> int:
    cases = [json.loads(p.read_text(encoding="utf-8")) for p in sorted((OUT / "cases").glob("*/*.json"))]
    judgments = []
    for c in cases:
        inferred = any("inferred" in (m.get("title") or "") for m in c["context"]["state"]["buying_group"])
        for e in c["expected"]:
            why = [r for r, hit in (("the two passes disagreed", e.get("debatable")), ("it blocks (check against the catalog rule text)", e["blocking"]),
                                    ("hard case", c["difficulty"] == "hard"), ("analogue case (see evidence_gap)", bool(c.get("evidence_gap"))),
                                    ("debatable: depends on a role CRMArena does not record (champion or buying-group role inferred)",
                                     inferred and e["eval_type"] in ROLE_EVALS)) if hit]
            if why:
                judgments.append({"case": c["id"], "eval_type": e["eval_type"], "verdict": e["verdict"], "why": why})
    body = {
        "label_kind": "model labels (one author, two passes), not human labels",
        "human_agreement": "None exists. HAR-97 L5 (human agreement) stays blocked until people label this gold; the numbers in "
                           "bench/reports/crmarena-gold-agreement-2026-10-02.json are one author's self-agreement, clustered by deal.",
        "all_judgments_need": ["an independent human label", "a blind second-family LLM label"],
        "judgments": judgments,
        "legacy_contested": [{"case": "beta_cp4_nonmaterial_fyi_no_action", "eval_type": "next_action_quality",
                              "why": "adjudicated 2-2 tie broken by C; reverses the original rule that this trigger's run does not own the open residency commitment"}],
        "cases_with_inferred_champion": sorted(c["id"] for c in cases if any("champion" in m["roles"] for m in c["context"]["state"]["buying_group"])),
        "cases_with_evidence_gap": sorted(c["id"] for c in cases if c.get("evidence_gap")),
        "inferred_roles_in_the_context": "judges see 'champion role inferred, not recorded' / 'role inferred from department' in the member title (the schema has no confidence field and the judge code is unchanged)",
        "open_items": [
            "valid_champion_handoff has no real instance in CRMArena (no delegation, no departure); replace its four analogues with WP32 synthetic or human-built cases",
            "champion, buying-group roles, economic buyer, stage and risk are not in the record; state fields are hand-derived from events up to the decision time (WP6 was not run on this data)",
            "candidate actions are authored drafts, not agent output; label a sample of real agent drafts on the same moments",
            "blocking follows the catalog rule text (bench/labels/crmarena/blocking_rule_review.json); a human should confirm each blocking judgment listed above",
        ],
    }
    (ROOT / "bench" / "labels" / "crmarena" / "labels_needed.json").write_text(json.dumps(body, indent=1, ensure_ascii=False) + NL, encoding="utf-8", newline=NL)
    print(f"{len(judgments)} judgments listed")
    return 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="cmd", required=True)
    sheets = sub.add_parser("sheets")
    sheets.add_argument("--out", required=True)
    for name in ("build", "agree", "verify", "needed"):
        sub.add_parser(name)
    args = parser.parse_args(argv)
    return {"sheets": cmd_sheets, "build": cmd_build, "agree": cmd_agree, "verify": cmd_verify, "needed": cmd_needed}[args.cmd](args)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
