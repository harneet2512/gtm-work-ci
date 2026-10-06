"""Generator entry point. Generation is refused until the rule set is frozen and preflight passes.

There is no --seed option: generation uses rules.v1.json#generation_seed only. If a QA gate fails,
the rules or process parameters are fixed (and the change recorded); the seed is never shopped.

Pipeline (docs/data/synthetic-layer-v1.md v1.1; pipeline.generate_to). Time runs FORWARD per deal (forward.py):
nothing dated before a deal's close is a function of its outcome.

  0. preflight     rule set frozen, power report current (rules hash), clean sweep gates pass (power_sweep)
  1. load_base     read the WP31 export (read-only, real customer emails included); hash it into the manifest
  2. assign_roles  buying-group roles from titles (roles.py)                       -> labels/roles.jsonl
  3. simulate      per deal: exogenous plan (plan.py) then the forward kernel (forward.py)
  4. render        kernel records -> SourceEvents (render.py; Salesforce-shaped ids, marker on the envelope only)
                                                                                    -> events/, labels/*.jsonl
  5. qa_gates      win rate, oracle/learner/hidden/null gates on the realised data (power_sim.gate_failures),
                   id-collision check, leakcheck.scan_generated(events/) (rule text, visible markers)
  6. manifest      seed, rules hash, base hash, counts, gate results

Run: python -m synthetic.generate --base <wp31 export> --out data/synthetic/v1 (the out directory must be new or empty)
"""
from __future__ import annotations

import argparse
import json
import sys
from collections.abc import Sequence
from pathlib import Path

from . import paraphrase, rules
from .pipeline import generate_to
from .power_sweep import REAL_REPORT_PATH, REPORT_PATH

STAGES = ("load_base", "assign_roles", "simulate", "render", "qa_gates", "manifest")


def parse(argv: Sequence[str]) -> argparse.Namespace:
    p = argparse.ArgumentParser(prog="synthetic.generate", description=__doc__.splitlines()[0])
    p.add_argument("--base", required=True, help="WP31 export of the CRMArena-Pro org (read-only)")
    p.add_argument("--out", required=True)
    p.add_argument("--paraphrase", choices=("off", "collect", "replay"), default="off",
                   help="realism pass: collect requests, or replay recorded cassettes (python -m synthetic.paraphrase run)")
    p.add_argument("--allow-template-fallback", action="store_true",
                   help="replay: use the template text for a missing or invalid cassette instead of failing (counted)")
    p.add_argument("--paraphrase-dir", default="data/synthetic/paraphrase_v1")
    return p.parse_args(list(argv))


def accepted(failure: str, doc: dict, base: str, mode: str) -> bool:
    """True only for a sweep failure that EXACTLY matches a recorded gate exception (rules.v1.json#gate_exceptions:
    `failure` or one of `failures`) AND whose `base` (stand_in / real_snapshot) and `mode` equal those of the
    sweep that produced it. Nothing else is waived: no threshold is lowered and no other failure is bypassed; a
    failure that names a hidden rule or a null gate can never be recorded (rules.load_rules refuses it)."""
    return any((failure == e.get("failure") or failure in e.get("failures", ())) and e.get("base") == base
               and e.get("mode") == mode for e in doc.get("gate_exceptions", []))


def preflight(report_path: Path = REAL_REPORT_PATH, rules_path: Path = rules.RULES_PATH) -> list[str]:
    """Reasons generation must not run: a draft or unfrozen rule set, a frozen hash that no longer matches, a
    missing or stale power report (its rules hash differs from the rule file), or a failed clean-sweep gate
    that is not an exact recorded exception. The gating report is the REAL-base sweep (synthetic-power-real-v1.json);
    the stand-in sweep (synthetic-power-v1.json) is informational."""
    rule_set, doc = rules.load_rules(rules_path), rules.load_doc(rules_path)
    reasons = []
    if rule_set.status == "frozen" and doc.get("frozen_sha256") != rules.content_hash(doc):
        reasons.append("the rule file changed after it was frozen (frozen_sha256 mismatch): make a new version")
    if rule_set.status != "frozen":
        reasons.append(f"{rule_set.version} is a draft: generation waits for spec review of "
                       "docs/data/synthetic-layer-v1.md (then freeze the rule file)")
    if not report_path.exists():
        return reasons + [f"no power report at {report_path}: run python -m synthetic.power_sweep --base <export>"]
    report = json.loads(report_path.read_text(encoding="utf-8"))
    if report["rules_sha256"] != rules.content_hash(doc):
        reasons.append("the power report is stale (rules changed since it was produced): rerun the sweep")
    base = report.get("base", "")
    reasons += [f"sweep gate: {f}" for f in report["clean"]["sweep_failures"] if not accepted(f, doc, base, "clean")]
    return reasons


def main(argv: Sequence[str] | None = None) -> int:
    args = parse(sys.argv[1:] if argv is None else argv)
    reasons = preflight()
    if reasons:
        print("generation refused (spec review / gates):\n  " + "\n  ".join(reasons), file=sys.stderr)
        return 2
    out = Path(args.out)
    if out.exists() and any(out.iterdir()):
        print(f"generation refused: {out} is not empty (stale files would mix into the manifest)", file=sys.stderr)
        return 2
    vocab = paraphrase.real_pool_and_vocab(Path(args.base))[1] if args.paraphrase == "replay" else ()
    manifest = generate_to(Path(args.base), out, rules.load_rules(), rules.load_doc(),
                             None if args.paraphrase == "off" else (args.paraphrase, Path(args.paraphrase_dir)),
                             vocab, args.allow_template_fallback)
    print(json.dumps({k: manifest[k] for k in ("deals", "covered_deals", "events", "manifest_sha256",
                                               "realised_gate_failures", "paraphrase")}, indent=1))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
