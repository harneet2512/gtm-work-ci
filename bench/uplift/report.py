"""Assemble and verify abc_report.json (contracts/har129/abc_report.v1.json).

Every aggregate is a pure function of the per-situation arm records (`aggregate`), so a report can be re-verified from
its own `situations` (`verify`): the numbers in a report that disagree with its situations are refused. No model call
and no LLM judge is involved anywhere here.
"""
from __future__ import annotations

import hashlib
import json
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

from bench.synthetic.caveats import ABC_CAVEATS

from . import scoring
from .stats import count_pairs, paired_stat, sign_test_p

ROOT = Path(__file__).resolve().parents[2]
SCHEMA_PATH = ROOT / "contracts" / "har129" / "abc_report.v1.json"
SIGNIFICANCE = 0.05
RATED = ("discriminating", "control")  # kinds where the hidden rule rates the choice; an exception carries no planted effect
EFFECT_FLOOR = 0.5  # C is 'ignored' only if its mean difference from A is below half the smaller planted effect (1.0)
HAR131_CAVEAT = ("HAR-131: this proves the loop recovers planted rules from previous deals and applies them correctly; "
                 "it does not prove the rules hold in real selling.")
EXTRACTOR_CAVEAT = ("Ground-truth state: every arm decides on an account state built through the real ingest and recompute path, "
                    "but the planted facts in customer emails (an objection, a blocker) are supplied by a deterministic table "
                    "instead of the model extractor, so extraction error is not measured here (see the noisy-mode caveat).")
SCALE_CAVEAT = ("Scale and sampling: one sample per arm per situation from a free OpenRouter model, on the few situations the "
                "lifecycle and the freshness window leave; the paired tests are small-n and a non-significant result is not "
                "evidence of no effect. The decision classifier is a fixed lexicon over the candidate's strategy type, title "
                "and description; it was written before any model output was seen.")
SCORING_NOTE = ("A decision is the account agent's preferred first-draft candidate (worker ranking 1, before revision). It is "
                "classified from the candidate's strategy type, title and one-line description by a fixed lexicon, and scored "
                "as the hidden-only planted log-odds of the option it realises (changing the quoted amount: -1.0; sending a "
                "separate second quote: -1.1), 0 otherwise. No LLM judge is used for any number.")
SELECTION_NOTE = ("Situations are test-split decision moments chosen from rendered events by decision relevance only (the hidden "
                  "rule rates the choice, or its precondition fails), inside the window in which the learned items are fresh "
                  "under the lifecycle's stale rule, in a fixed seeded order. Neither an outcome nor an arm result was read.")
ARM_TEXT = {"A": "same account state, no learned knowledge (the knowledge-withheld counterfactual of E7)",
            "B": "same account state plus applicable learned knowledge",
            "C": "same account state plus irrelevant knowledge"}


def caveats() -> list[str]:
    return [text for _, text in ABC_CAVEATS] + [EXTRACTOR_CAVEAT, SCALE_CAVEAT, HAR131_CAVEAT]


def draft_sha256(candidate: Mapping[str, Any]) -> str:
    art = candidate.get("full_action_artifact") or {}
    body = json.dumps({"to": sorted(r["person_id"] for r in candidate.get("to") or []),
                       "cc": sorted(r["person_id"] for r in candidate.get("cc") or []),
                       "subject": art.get("subject"), "body": art.get("body"), "channel": art.get("channel"),
                       "action_type": candidate.get("action_type")}, sort_keys=True, ensure_ascii=False)
    return hashlib.sha256(body.encode("utf-8")).hexdigest()


def arm_record(point: str, kind: str, raw: Mapping[str, Any]) -> dict[str, Any]:
    """The report's `arm` of one arm of one situation, from the Go arm output."""
    cand = raw["candidate"]
    cls = scoring.classify(point, cand)
    score, regret = scoring.decision_score(point, cls["option"]) if kind in RATED else (0.0, 0.0)
    know = raw["knowledge"]
    recipients = [{"person_id": r["person_id"], "role": "to"} for r in cand.get("to") or []] + \
                 [{"person_id": r["person_id"], "role": "cc"} for r in cand.get("cc") or []]
    return {
        "candidate_id": cand["candidate_id"], "strategy_type": cand["strategy_type"], "action_type": cand["action_type"],
        "recipients": recipients, "realized_option": cls["option"], "followed_recommendation": cls["recommendation"],
        "action_evidence": cls["evidence"], "decision_score": score, "regret": regret,
        "corrections_needed": len(raw["corrections"]),
        "knowledge_retrieved": sorted(know["retrieved"]), "knowledge_applicable": sorted(know["applicable"]),
        "knowledge_exception_blocked": sorted(know["exception_blocked"]), "knowledge_cited": sorted(know["cited"]),
        "draft_sha256": draft_sha256(cand),
    }


def _choice(arm: Mapping[str, Any]) -> tuple[bool, bool]:
    return arm["realized_option"], arm["followed_recommendation"]


def _pair(rows: Sequence[Mapping[str, Any]], first: str, second: str, seed: int) -> dict[str, Any]:
    rows = [s for s in rows if first in s["arms"] and second in s["arms"]]
    return paired_stat([s["arms"][first]["decision_score"] for s in rows], [s["arms"][second]["decision_score"] for s in rows],
                       [_choice(s["arms"][first]) for s in rows], [_choice(s["arms"][second]) for s in rows], seed)


def _transfer(n: int, worse: int) -> dict[str, Any]:
    return {"n": n, "n_worse": worse, "rate": round(worse / n, 6) if n else 0.0}


def _corrections(sits: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    out: dict[str, Any] = {"proxy": ("blocking failures of the in-core deterministic evals on the preferred first draft; "
                                      "a proxy, no human edit is observed")}
    for arm in "ABC":
        counts = [s["arms"][arm]["corrections_needed"] for s in sits if arm in s["arms"]]
        out[arm] = {"total": sum(counts), "mean": round(sum(counts) / len(counts), 6) if counts else 0.0}
    both = [s for s in sits if "A" in s["arms"] and "B" in s["arms"]]
    better, worse, ties = count_pairs([-s["arms"]["B"]["corrections_needed"] for s in both],  # fewer corrections = higher
                                      [-s["arms"]["A"]["corrections_needed"] for s in both])
    mean_b = sum(s["arms"]["B"]["corrections_needed"] for s in both) / len(both) if both else 0.0
    mean_a = sum(s["arms"]["A"]["corrections_needed"] for s in both) / len(both) if both else 0.0
    out["B_minus_A"] = round(mean_b - mean_a, 6)
    out["B_vs_A"] = {"n": len(both), "first_better": better, "second_better": worse, "ties": ties,
                     "sign_test_p": sign_test_p(better, worse)}
    return out


def _exceptions(sits: Sequence[Mapping[str, Any]], target: Mapping[str, str]) -> dict[str, Any]:
    exc = [s for s in sits if s["kind"] == "exception"]
    rows = [(s, target.get(s["decision_point"])) for s in exc]
    retrieved = [(s, k) for s, k in rows if k in s["arms"]["B"]["knowledge_retrieved"]]
    not_applicable = [(s, k) for s, k in retrieved if k not in s["arms"]["B"]["knowledge_applicable"]]
    diffs = [s["arms"]["B"]["decision_score"] - s["arms"]["A"]["decision_score"] for s in exc]
    return {
        "n": len(exc), "retrieved": len(retrieved), "marked_not_applicable": len(not_applicable),
        "applied": sum(1 for s, k in rows if k in s["arms"]["B"]["knowledge_cited"]),
        "misapplied_by_B": sum(1 for s in exc if s["arms"]["B"]["followed_recommendation"]),
        "misapplied_by_A": sum(1 for s in exc if s["arms"]["A"]["followed_recommendation"]),
        "b_equals_a": sum(1 for s in exc if _choice(s["arms"]["B"]) == _choice(s["arms"]["A"])),
        "mean_score_b_minus_a": round(sum(diffs) / len(diffs), 6) if diffs else 0.0,
    }


def _controls(sits: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    ctl = [s for s in sits if s["kind"] == "control"]
    diffs = [s["arms"]["C"]["decision_score"] - s["arms"]["A"]["decision_score"] for s in ctl]
    retrieved = [s for s in ctl if s["arms"]["C"]["knowledge_retrieved"]]
    return {
        "n": len(ctl), "retrieved": len(retrieved),
        "marked_not_applicable": sum(1 for s in retrieved if not s["arms"]["C"]["knowledge_applicable"]),
        "applied": sum(1 for s in ctl if s["arms"]["C"]["knowledge_cited"]),
        "c_equals_a": sum(1 for s in ctl if _choice(s["arms"]["C"]) == _choice(s["arms"]["A"])),
        "mean_score_c_minus_a": round(sum(diffs) / len(diffs), 6) if diffs else 0.0,
    }


def _headline(paired: Mapping[str, Any], exc: Mapping[str, Any], ctl: Mapping[str, Any], transfer: Mapping[str, Any]) -> dict[str, Any]:
    ba, ca = paired["B_vs_A"], paired["C_vs_A"]
    b_better = ba["n"] > 0 and ba["mean_difference"] > 0 and ba["sign_test_p"] < SIGNIFICANCE
    c_ignored = (ctl["n"] > 0 and ca["sign_test_p"] >= SIGNIFICANCE and abs(ca["mean_difference"]) < EFFECT_FLOOR
                 and ctl["applied"] == 0)
    respected = (exc["n"] > 0 and exc["retrieved"] == exc["n"] and exc["marked_not_applicable"] == exc["n"]
                 and exc["applied"] == 0 and exc["misapplied_by_B"] <= exc["misapplied_by_A"])
    text = (f"B vs A over {ba['n']} discriminating situations: mean decision score {ba['mean_score_first']:+.3f} vs "
            f"{ba['mean_score_second']:+.3f} (difference {ba['mean_difference']:+.3f}, 95% CI {ba['ci95'][0]:+.3f} to "
            f"{ba['ci95'][1]:+.3f}), B better in {ba['wins']}, worse in {ba['losses']}, tied in {ba['ties']}, sign test "
            f"p = {ba['sign_test_p']:.4f}: " + ("B is better than A." if b_better else "B is NOT shown better than A.")
            + f" C vs A over {ca['n']} control situations: difference {ca['mean_difference']:+.3f}, p = {ca['sign_test_p']:.4f}, "
            + f"same decision in {ca['share_same_decision']:.0%}: " + ("C is ignored." if c_ignored else "C is NOT shown to be ignored.")
            + f" Negative transfer: {transfer['n_worse']} of {transfer['n']} situations ({transfer['rate']:.1%}). "
            + f"Exception situations: {exc['retrieved']} of {exc['n']} retrieved, {exc['marked_not_applicable']} marked not "
            + f"applicable, {exc['applied']} applied; B misapplied the learned course in {exc['misapplied_by_B']} vs "
            + f"{exc['misapplied_by_A']} for A: " + ("the exception was respected." if respected else "the exception was NOT shown respected."))
    return {"b_better_than_a": b_better, "c_ignored": c_ignored, "exception_respected": respected, "text": text}


def aggregate(sits: Sequence[Mapping[str, Any]], seed: int, target: Mapping[str, str]) -> dict[str, Any]:
    """paired, negative_transfer, corrections, exceptions, controls and headline: pure functions of the situations."""
    disc = [s for s in sits if s["kind"] == "discriminating"]
    ctl = [s for s in sits if s["kind"] == "control"]
    rated = [s for s in sits if s["kind"] in RATED]
    paired = {"B_vs_A": _pair(disc, "B", "A", seed), "B_vs_C": _pair(rated, "B", "C", seed), "C_vs_A": _pair(rated, "C", "A", seed)}
    exc, controls = _exceptions(sits, target), _controls(sits)
    worse_d = sum(1 for s in disc if s["arms"]["B"]["decision_score"] < s["arms"]["A"]["decision_score"] - 1e-9)
    worse_e = sum(1 for s in sits if s["kind"] == "exception"
                  and s["arms"]["B"]["followed_recommendation"] and not s["arms"]["A"]["followed_recommendation"])
    worse_c = sum(1 for s in ctl if s["arms"]["C"]["decision_score"] < s["arms"]["A"]["decision_score"] - 1e-9)
    transfer = {**_transfer(len(sits), worse_d + worse_e + worse_c), "discriminating": _transfer(len(disc), worse_d),
                "exception": _transfer(exc["n"], worse_e), "control": _transfer(len(ctl), worse_c)}
    return {"paired": paired, "negative_transfer": transfer, "corrections": _corrections(sits), "exceptions": exc,
            "controls": controls, "headline": _headline(paired, exc, controls, transfer)}


def target_ids(report: Mapping[str, Any]) -> dict[str, str]:
    """decision point -> the learned item about it (the item an exception situation must not apply)."""
    return {k["decision_point"]: k["id"] for k in report["learning"]["knowledge"] if k["origin"] == "learned"}


def verify(report: Mapping[str, Any]) -> list[str]:
    """Problems with a report: schema violations, then any aggregate that is not what its situations give."""
    from jsonschema import Draft202012Validator, FormatChecker
    from referencing import Registry, Resource

    docs = [json.loads(p.read_text(encoding="utf-8")) for p in (ROOT / "contracts" / "schemas").glob("*.json")]
    registry = Registry().with_resources([(d["$id"], Resource.from_contents(d)) for d in docs])
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    problems = [e.message for e in Draft202012Validator(schema, registry=registry, format_checker=FormatChecker()).iter_errors(report)]
    if problems:
        return problems
    again = aggregate(report["situations"], report["provenance"]["seed"], target_ids(report))
    problems += [f"{key} does not match its situations" for key in again if again[key] != report[key]]
    problems += [f"counts: experiment says {report['experiment'][k]} {k[2:]} situations, found {n}"
                 for k, n in (("n_discriminating", sum(s["kind"] == "discriminating" for s in report["situations"])),
                              ("n_exception", sum(s["kind"] == "exception" for s in report["situations"])),
                              ("n_control", sum(s["kind"] == "control" for s in report["situations"])))
                 if report["experiment"][k] != n]
    return problems


def _hypothesis(h: Mapping[str, Any]) -> dict[str, Any]:
    keys = ("decision_point", "kind", "n_option", "n_other", "win_rate_option", "win_rate_other", "p_value", "promoted")
    return {k: h[k] for k in keys}


def utc(timestamp: str) -> str:
    """An RFC 3339 time as UTC with a Z, whatever offset the database wrote it with."""
    from datetime import datetime, timezone

    return datetime.fromisoformat(timestamp.replace("Z", "+00:00")).astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def lifecycle_note(learning: Mapping[str, Any], store: Sequence[Mapping[str, Any]]) -> str:
    """What the real lifecycle did to each statistically promoted item, in words (status and the lifecycle's own reason)."""
    by_id = {k["id"]: k for k in store}
    parts = []
    for item in learning["knowledge"]:
        got = by_id.get(item["id"])
        if got is None:
            parts.append(f"{item['decision_point']}: not in the store")
            continue
        history = got.get("status_history") or []
        reason = f" ({history[-1]['reason']})" if history and got["status"] in ("disputed", "stale") else ""
        parts.append(f"{got['key']} {item['decision_point']}: {got['status']}{reason}")
    return "; ".join(parts) or "no item was promoted"


def build(*, arms: Mapping[str, Any], pack: Mapping[str, Any], learning: Mapping[str, Any], store: Sequence[Mapping[str, Any]],
          guard: Mapping[str, Any], provenance: Mapping[str, Any], seed: int, rule_set=None) -> dict[str, Any]:
    """The full abc_report.v1 from the Go arm output, the frozen pack and learning evidence, and the exported store."""
    by_id = {s["id"]: s for s in arms["situations"]}
    sits = []
    for ps in pack["situations"]:
        raw = by_id[ps["id"]]["arms"]
        point, kind = ps["decision_point"], ps["kind"]
        sits.append({
            "id": ps["id"], "kind": kind, "decision_point": point, "deal_id": ps["deal_id"],
            "trigger_time": ps["trigger"]["occurred_at"], "selection_reason": ps["selection_reason"],
            "rule_value": scoring.rule_value(point, rule_set) if kind in RATED else 0.0,
            "arms": {arm: arm_record(point, kind, raw[arm]) for arm in "ABC" if raw.get(arm)}})
    knowledge = [{"id": k["id"], "key": k["key"], "title": k["title"],
                  "decision_point": next(h["decision_point"] for h in learning["knowledge"] if h["id"] == k["id"]),
                  "status": k["status"], "counts": k["counts"], "created_at": utc(k["created_at"]), "origin": "learned"}
                 for k in store]
    created = max(k["created_at"] for k in knowledge) if knowledge else utc(learning["cutoff"])
    first = min(s["trigger_time"] for s in sits) if sits else utc(learning["cutoff"])
    controls = sum(1 for h in learning["hypotheses"] if h["promoted"] and h["kind"] == "control")
    report = {
        "version": "abc_report.v1",
        "experiment": {"id": "abc-uplift-v1", "arms": ARM_TEXT, "primary_metric": "hidden_rule_decision_score",
                       "scoring": SCORING_NOTE, "selection": SELECTION_NOTE,
                       "n_discriminating": sum(s["kind"] == "discriminating" for s in sits),
                       "n_exception": sum(s["kind"] == "exception" for s in sits),
                       "n_control": sum(s["kind"] == "control" for s in sits)},
        "provenance": dict(provenance), "caveats": caveats(),
        "learning": {"cutoff": learning["cutoff"][:10], "previous_deals": learning["previous_deals"],
                     "hypotheses_tested": [_hypothesis(h) for h in learning["hypotheses"]],
                     "promotion_rule": learning["promotion_rule"], "knowledge": knowledge, "null_false_promotions": controls,
                     "guard": dict(guard), "lifecycle_note": lifecycle_note(learning, store),
                     "no_leakage": {"latest_knowledge_created_at": created, "earliest_trigger_time": first,
                                    "passed": created < first}},
        "situations": sits,
    }
    report.update(aggregate(sits, seed, {k["decision_point"]: k["id"] for k in knowledge}))
    return report
