"""Render docs/metrics.md from contracts/metrics/metric_registry.json (HAR-132 / WP33, HAR-140 / WP36). Generated; never hand-edited."""
from __future__ import annotations

from collections import Counter

LAYER_TITLES = {
    "trigger": "1. Trigger: should the agent have run?",
    "state_context_L1": "2. L1 state / context",
    "eval_routing": "3. Eval routing by transition status",
    "deterministic_evals": "4. Deterministic evals (§4) and sub-checks",
    "semantic_evals": "5. Semantic evals (§5) and rep-style dimensions (§24)",
    "decision_L2": "6. L2 GTM decision (§15(GTM)) and criteria",
    "decision_ranking": "7. Decision ranking",
    "abstention": "8. Abstention",
    "trajectory_L3": "9. L3 trajectory / execution",
    "knowledge_L4": "10. L4 knowledge: extraction, applicability, uplift, negative transfer",
    "edit_propagation": "10b. Edit / override propagation (canonical E11)",
    "meta_L5": "11. L5 meta-evals: grader vs human",
    "benchmark_cases": "12. §20 offline benchmark: required case types",
    "eval_quality": "13. Evaluation of the evals (§20 offline, §21 online, §22)",
    "human_feedback": "14. Human feedback (§8, §12 immediate)",
    "online_product": "15. Online product evaluation",
    "customer_reaction": "16. Customer reaction (§12 fast)",
    "business_outcome": "17. Business outcome (§12 slow)",
    "slices": "18. Regression slices (dimensions)",
    "integration_gate": "19. HAR-129 integration gates (derived)",
}
STATUSES = ("measured", "computable", "blocked")


def cell(text: object) -> str:
    return str(text).replace("|", "\\|").replace("\n", " ")


def value_or_blocker(m: dict) -> str:
    if m["status"] == "measured":
        v = m["current_value"]
        caveat = f" Caveat: {v['caveat']}" if v.get("caveat") else ""
        return f"**{v['display']}** ({v['source']}).{caveat}"
    tracked = f" (tracked by {', '.join(m['tracked_by'])})"
    if m["status"] == "blocked":
        return f"blocked: {m['blocker']}{tracked}"
    return f"computable: {m.get('notes', 'inputs exist; not run yet')}{tracked}"


def data_basis(m: dict) -> str:
    return m["current_value"]["data_basis"] if m["status"] == "measured" else "-"


def ref_text(m: dict) -> str:
    r = m["har97_ref"]
    return f"{r['source']} {r['section']}: \"{r['quote']}\""


def summary_table(registry: dict) -> list[str]:
    counts = Counter((m["layer"], m["status"]) for m in registry["metrics"])
    lines = ["| Layer | measured | computable | blocked | total |", "|---|---|---|---|---|"]
    for layer in registry["layer_order"]:
        row = [counts[(layer, s)] for s in STATUSES]
        lines.append(f"| {LAYER_TITLES[layer]} | {' | '.join(map(str, row))} | {sum(row)} |")
    totals = [sum(counts[(layer, s)] for layer in registry["layer_order"]) for s in STATUSES]
    lines.append(f"| **total** | {' | '.join(map(str, totals))} | {sum(totals)} |")
    return lines


def kind_line(registry: dict) -> str:
    kinds = Counter(m["kind"] for m in registry["metrics"])
    order = ("named_metric", "eval_check", "supervision_signal", "benchmark_case_type", "derived", "canonical")
    return ", ".join(f"{k} {kinds[k]}" for k in order)


def basis_line(registry: dict) -> str:
    basis = Counter(data_basis(m) for m in registry["metrics"] if m["status"] == "measured")
    return ", ".join(f"{k} {basis[k]}" for k in sorted(basis))


FIRST_DOC, SECOND_DOC = "docs/metrics.md", "docs/metrics-learning-outcomes.md"
SPLIT_AT = "knowledge_L4"  # first layer of the second file


def _status_mix(registry: dict, ids: list[str]) -> str:
    by = {m["id"]: m["status"] for m in registry["metrics"]}
    mix = Counter(by[i] for i in ids)
    return ", ".join(f"{mix[s]} {s}" for s in STATUSES if mix[s])


def headline_table(registry: dict) -> list[str]:
    out = ["## Headline product metrics (HAR-97 canonical section 9)", "",
           "The product scorecard is these 11 items; everything else is a diagnostic that explains why one of them moved. "
           "`replaces` is the frontier-tier P1-P7 item an entry succeeds.", "",
           "| # | headline | metrics | status of its metrics | evals | replaces |", "|---|---|---|---|---|---|"]
    for h in registry["headline_set"]:
        ids = h["metric_ids"]
        shown = ", ".join(f"`{i}`" for i in ids) if len(ids) <= 4 else f"{len(ids)} metrics, `{ids[0]}` ... `{ids[-1]}`"
        out.append("| " + " | ".join(cell(x) for x in (
            h["rank"], h["name"], shown, _status_mix(registry, ids), ", ".join(h["evals"]), h["replaces"] or "-")) + " |")
    return [*out, ""]


def operational_table(registry: dict) -> list[str]:
    out = ["## Operational diagnostics (HAR-97 canonical section 8)", "",
           "Engineering measurements, a separate class from the learning metrics above: they never mutate GTM company knowledge "
           "(tested), and optimization is allowed only subject to quality floors.", "",
           "| id | diagnostic | status | measures | consumers | owner |", "|---|---|---|---|---|---|"]
    for o in registry["operational_diagnostics"]:
        out.append("| " + " | ".join(cell(x) for x in (
            f"`{o['id']}`", o["name"], f"{o['status']}: {o['blocker']} (tracked by {', '.join(o['tracked_by'])})",
            "; ".join(o["measures"]), ", ".join(o["consumers"]), o["owner_wp"])) + " |")
    return [*out, ""]


def layer_tables(registry: dict, layers: list[str]) -> list[str]:
    out: list[str] = []
    for layer in layers:
        out += [f"## {LAYER_TITLES[layer]}", "",
                "| id | metric | kind | status | data_basis | value / blocker | formula | HAR-97 ref | owner |",
                "|---|---|---|---|---|---|---|---|---|"]
        for m in (x for x in registry["metrics"] if x["layer"] == layer):
            f = m["formula"]
            formula = f.get("procedure") or f"{f['numerator']} ÷ {f['denominator']}"
            out.append("| " + " | ".join(cell(x) for x in (
                f"`{m['id']}`", m["name"], m["kind"], m["status"], data_basis(m), value_or_blocker(m), formula,
                ref_text(m), m["owner_wp"])) + " |")
        out.append("")
    return out


def render_docs(registry: dict) -> dict[str, str]:
    """Return {repo path: markdown}; the layers are split at SPLIT_AT so each file stays under 400 lines."""
    src = registry["source"]
    out = ["# HAR-97 metrics", "",
           "Generated from `contracts/metrics/metric_registry.json` by `python bench/metrics/report.py --docs`; do not "
           "edit by hand. Groups run from process metrics (trigger, state, decision, trajectory) to final state and "
           "outcome metrics, split over two files to stay small: this file (process layers) and "
           f"`{SECOND_DOC}` (knowledge, meta-evals, supervision and outcomes). The latest measured values are in "
           "`bench/reports/metrics-<date>.md`.", "",
           f"Source: HAR-97 as of {src['updated_at']} (sha256 `{src['sha256'][:12]}…`); every line HAR-97 marks "
           f"acknowledged is an entry or sub-entry (snapshot `{src['acknowledged_lines']}`).", "",
           "**Kind:** named_metric = in one of HAR-97's own Measure lists; eval_check = a HAR-97 check or question "
           "tracked as a pass rate; supervision_signal = a §8 human outcome or §12 reaction/outcome; benchmark_case_type = "
           "a §20 required gold situation (measured as coverage); derived = not a HAR-97 metric (premature promotion, "
           "HAR-129 gates); canonical = a headline-only or frontier (V5, V6) metric from the CANONICAL EVAL STRATEGY, not an "
           "acknowledged HAR-97 line. Sub-entries name their parent in the id (e.g. `det.recipient_correctness.recipients_exist`).",
           "",
           "**Status:** measured = value in the repo with its source; computable = data, gold and code exist, only "
           "the run or scorer is missing; blocked = a component, data or gold is missing (see blocker). Every "
           "computable or blocked entry names the Linear issue that tracks it.", "",
           "**data_basis** (measured values): legacy_fixture_world = the invented Acme/Beta/Northstar fixture "
           "accounts, not real data; live_model_on_legacy_situations = a live model on situations built from them; "
           "crmarena_b2b / crmarena_b2b+synthetic_v1 = CRMArena-Pro B2B deals (HAR-130) and their labelled synthetic "
           "layer (HAR-131).", "",
           "## Summary", "", *summary_table(registry), "", f"By kind: {kind_line(registry)}.", "",
           f"Measured values by data_basis: {basis_line(registry)}.", "", *headline_table(registry)]
    order = registry["layer_order"]
    split = order.index(SPLIT_AT)
    second = ["# HAR-97 metrics: knowledge, meta-evals, supervision and outcomes", "",
              f"Continuation of `{FIRST_DOC}` (legend and summary there); generated by "
              "`python bench/metrics/report.py --docs`, do not edit by hand.", ""]
    return {FIRST_DOC: "\n".join(out + layer_tables(registry, order[:split])),
            SECOND_DOC: "\n".join(second + layer_tables(registry, order[split:]))}
