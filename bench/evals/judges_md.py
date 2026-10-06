"""Markdown rendering of a run_judges report (one dict in, one string out; no I/O)."""
from __future__ import annotations

from typing import Any

HEAD_KEYS = ("n_gold", "n_judged", "coverage", "verdict_agreement", "verdict_kappa", "label_agreement",
             "fail_precision", "fail_recall", "false_pass_rate", "false_block_rate", "block_recall", "abstain_rate")
# Shown on every agreement report: what the figures are measured against (contracts/evals/eval_registry.json gold_source).
REFERENCE_ANSWERS = "Reference answers by a stronger model, not human."
EVAL_KEYS = ("n_judged", "error_count", "skipped_count", "verdict_agreement", "label_agreement", "fail_precision", "fail_recall",
             "false_pass_rate", "false_block_rate", "diagnostic_recall")


def fmt(value: Any) -> str:
    if value is None:
        return "-"
    if isinstance(value, float):
        return f"{value:.3f}"
    return str(value)


def table(header: list[str], rows: list[list[Any]]) -> str:
    lines = ["| " + " | ".join(header) + " |", "|" + "---|" * len(header)]
    lines += ["| " + " | ".join(fmt(v) for v in row) + " |" for row in rows]
    return "\n".join(lines)


def _insufficient(report: dict[str, Any]) -> list[str]:
    """No judge answered: no rate is meaningful, so none is printed (HAR-116 review: never a false-pass 1.000)."""
    o = report["overall"]
    return [
        f"# INSUFFICIENT DATA: no judged rows ({report.get('title') or report['mode'] + ', ' + report['model']})",
        "",
        f"Gold: **{report['gold']}**. {report['n_cases']} cases, selection `{report['selection']}`, "
        f"{report['trials']} trial(s), generated {report['generated_at']}.",
        "",
        f"**{REFERENCE_ANSWERS}**",
        "",
        f"Of {o['n_gold']} gold judgments, 0 were judged: {o['error_count']} errored, {o['skipped_count']} skipped. "
        "No agreement, false-pass or false-block rate is reported because none can be computed.",
    ]


def _overview(report: dict[str, Any]) -> list[str]:
    o, c, k = report["overall"], report["case_level"], report["consistency"]
    return [
        "# " + (report.get("title") or f"Semantic judges benchmark ({report['mode']}, {report['model']})"),
        "",
        f"Gold: **{report['gold']}**. {report['n_cases']} cases, selection `{report['selection']}`, "
        f"{report['trials']} trial(s), prompt `{report['prompt_version']}`, catalog v{report['catalog_version']}, "
        f"generated {report['generated_at']}.",
        "",
        f"**{REFERENCE_ANSWERS}**",
        "",
        "## §20 metrics (pooled over gold judgments x trials)",
        table(list(HEAD_KEYS), [[o[key] for key in HEAD_KEYS]]),
        "",
        f"Case level: false-block rate {fmt(c['false_block_rate'])} over {c['good_runs']} should-pass runs; "
        f"false-pass rate {fmt(c['false_pass_rate'])} over {c['flawed_runs']} runs with a semantic gold fail "
        f"({fmt(c['false_pass_rate_any_flag'])} if a fail on any dimension counts as caught).",
        f"Repeat consistency: {fmt(k['unanimous_rate'])} unanimous, {fmt(k['pairwise_agreement'])} pairwise over "
        f"{k['n_repeated']} repeated (case, eval) pairs.",
    ]


def _per_eval(report: dict[str, Any]) -> list[str]:
    rows = [[name, report["evidence_class"][name], *[s[key] for key in EVAL_KEYS]]
            for name, s in report["per_eval"].items()]
    by_class = [[name, *[s[key] for key in EVAL_KEYS]] for name, s in report["by_evidence_class"].items()]
    return ["", "## Per eval (agreement and false-pass / false-block per judge)",
            table(["eval_type", "class", *EVAL_KEYS], rows),
            "", "## By evidence class (HAR-97 §15: not every eval is equally empirically proven)",
            table(["evidence_class", *EVAL_KEYS], by_class)]


def _l5(report: dict[str, Any]) -> list[str]:
    rows = [[r["dimension"], ", ".join(r["eval_types"]) or "-", r["gold_n"], r["gold_verdict_agreement"],
             r["gold_label_agreement"], r["gold_false_pass_rate"], r["gold_false_block_rate"], r["human_n"],
             r["human_agreement"], r["status"]] for r in report["l5"]]
    return ["", "## L5 grader agreement",
            table(["dimension", "eval types", "gold n", "gold agree", "gold label agree", "gold false-pass",
                   "gold false-block", "human n", "human agree", "status"], rows)]


def _slices(report: dict[str, Any]) -> list[str]:
    out = ["", "## Agreement by slice"]
    for tag, values in report["slices"].items():
        rows = [[value, s["n_judged"], s["verdict_agreement"], s["label_agreement"], s["false_pass_rate"],
                 s["false_block_rate"]] for value, s in values.items()]
        out += ["", f"### {tag}", table([tag, "n", "agree", "label agree", "false-pass", "false-block"], rows)]
    return out


def _missing(report: dict[str, Any]) -> list[str]:
    missing = report.get("missing_cassettes") or {}
    if not missing:
        return []
    rows = [[name, count] for name, count in missing.items()]
    return ["", f"## Rows without a cassette ({sum(missing.values())}; replay cannot judge them until a live re-run)",
            "Calls that never produced a cassette in the recorded run (provider failures), and rows whose prompt text "
            "changed after recording (the 3 rep_style rows: their criterion text was fixed).",
            table(["eval_type", "rows"], rows)]


def _routing(report: dict[str, Any]) -> list[str]:
    r = report["routing"]
    rows = [[a, s["cases"], s["mean_selected"], s["max_selected"]] for a, s in r["by_action"].items()]
    misses = [[m["case_id"], m["eval_type"], m["gold_verdict"], m["blocking"], m["reason"]] for m in r["missed_fails"]]
    return ["", "## §7 routing (no model calls)",
            f"Of {r['semantic_types']} semantic judges. Gold judgments routed: {fmt(r['recall_gold'])}; gold fails: "
            f"{fmt(r['recall_fail'])}; gold blocks: {fmt(r['recall_block'])}.",
            table(["proposed action", "cases", "mean judges", "max judges"], rows),
            "", "Gold fails the router would skip:",
            table(["case", "eval_type", "gold verdict", "gold blocking", "skip reason"], misses)]


def render(report: dict[str, Any]) -> str:
    if report["overall"]["insufficient_data"]:
        parts = _insufficient(report) + _missing(report) + _routing(report)
    else:
        parts = _overview(report) + _per_eval(report) + _l5(report) + _slices(report) + _missing(report) + _routing(report)
    if report["errors"]:
        reasons = report.get("error_reasons") or report["errors"]
        parts += ["", "## Judge errors", table(["error", "count"], [[k, v] for k, v in reasons.items()])]
    return "\n".join(parts) + "\n"
