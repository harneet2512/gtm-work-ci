"""Scoring logic of the live benchmarks (bench/live): pure functions, no model calls."""
from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench" / "live"))
sys.path.insert(0, str(ROOT / "worker-py" / "tests"))

import agent_causality  # noqa: E402
import extract_recall as er  # noqa: E402
from draft_situations import SITUATIONS  # noqa: E402

FACT = {"account": "acme", "evidence_event_file": "e.json", "field": "buying_group.member",
        "evidence_quote": "Marco runs security for the Berlin office"}


def claim(field: str, quote: str) -> dict:
    return {"field_path": field, "evidence_quote": quote}


def test_quote_match_accepts_containment_and_word_overlap_but_not_unrelated_text() -> None:
    assert er.quote_match("Marco runs security", FACT["evidence_quote"])
    assert er.quote_match("marco  runs SECURITY for the berlin office.", FACT["evidence_quote"])
    assert not er.quote_match("Pricing is due Friday", FACT["evidence_quote"])


def test_recall_modes_differ_only_in_how_strict_the_field_is() -> None:
    sibling = [claim("stakeholder_role", "Marco runs security for the Berlin office")]
    other = [claim("blockers", "Marco runs security for the Berlin office")]
    assert not er.recalled(FACT, sibling, "field")
    assert er.recalled(FACT, sibling, "fold")  # both fold into buying_group (claim.v1 fieldPath)
    assert not er.recalled(FACT, other, "fold")
    assert er.recalled(FACT, other, "quote")
    assert not er.recalled(FACT, [], "quote")


def test_missed_as_names_the_field_used_or_no_quote() -> None:
    assert er.missed_as(FACT, [claim("blockers", "Marco runs security")]) == ["blockers"]
    assert er.missed_as(FACT, [claim("blockers", "something else entirely")]) == ["<no quote>"]


def test_score_and_summary_report_strict_fold_and_quote_recall_with_misses() -> None:
    results = {"e.json": [{"claims": [claim("buying_group.member", FACT["evidence_quote"])]},
                          {"claims": [claim("stakeholder_role", FACT["evidence_quote"])]},
                          {"claims": []}]}
    per_fact = er.score([FACT], results)
    assert (per_fact[0]["field_hits"], per_fact[0]["fold_hits"], per_fact[0]["quote_hits"]) == (1, 2, 2)
    assert per_fact[0]["missed_as"] == {"stakeholder_role": 1, "<no quote>": 1}
    stats = {"calls": 3, "errors": 0, "claims": 2, "dropped": 0, "seconds": 3.0, "max_seconds": 2.0}
    report = er.summarize(per_fact, stats, 3, "m")
    assert (report["fact_recall"], report["fold_recall"], report["quote_recall"]) == (0.333, 0.667, 0.667)
    assert report["recalled_at_least_once"] == 1.0 and report["recalled_every_run"] == 0.0
    assert report["missed_as"] == {"buying_group.member": {"stakeholder_role": 1, "<no quote>": 1}}
    assert "fold_recall | 0.667" in er.markdown(report, per_fact)


def test_known_people_mark_the_seller_org_internal_and_the_account_external() -> None:
    people = er.known_people("acme")
    domain = "@" + er.org_domain()
    assert people and {p["internal"] for p in people} == {True, False}
    assert all(p["internal"] == p["raw_identity"].endswith(domain) for p in people)


def test_rejected_per_call_is_reported_and_tolerates_old_runs() -> None:
    stats = {"calls": 2, "errors": 0, "claims": 4, "dropped": 0, "rejected": 1, "seconds": 2.0, "max_seconds": 1.0}
    assert er.summarize([], stats, 1, "m")["rejected_per_call"] == 0.5
    del stats["rejected"]
    assert er.summarize([], stats, 1, "m")["rejected_per_call"] == 0.0


def test_fake_core_answers_every_context_tool_like_real_core() -> None:
    situation = SITUATIONS["beta_non_material"]
    packets = agent_causality.core_packets(situation)
    assert set(packets) == {"state", "recent_diffs", "evidence", "activities", "people", "commitments"}
    assert packets["state"] == situation.packets["state"]
    assert packets["people"] == []
