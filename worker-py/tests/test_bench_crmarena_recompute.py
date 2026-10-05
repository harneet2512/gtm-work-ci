"""bench/data/crmarena_extraction_recompute.py and the review-driven metrics: stage synonyms and in-force stage,
the chance baseline, claim flags, and the report rebuilt from a bundle alone."""
from __future__ import annotations

import gzip
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench" / "data"))

import crmarena_extraction_metrics as m  # noqa: E402
import crmarena_extraction_recompute as rc  # noqa: E402

SYNONYMS = json.loads((ROOT / "bench" / "data" / "crmarena_stage_synonyms.json").read_text(encoding="utf-8"))


@pytest.mark.parametrize("text,expected", [
    ("Contract Signed", "Closed"), ("closed won", "Closed"), ("final stage of agreement", "Closed"),
    ("contract negotiation", "Negotiation"), ("Negotiating terms", "Negotiation"),
    ("proposal development", "Quote"), ("Qualified lead", "Qualification"), ("Discovery call", "Discovery"),
    ("something unrelated", None), ("", None),
])
def test_canonical_stage_maps_synonyms_onto_the_crm_vocabulary(text: str, expected: str | None) -> None:
    assert m.canonical_stage(text, SYNONYMS) == expected


def test_synonym_agreement_is_looser_than_strict() -> None:
    assert not m.stage_agrees("Contract Signed", "Closed")
    assert m.stage_agrees_synonym("Contract Signed", "Closed", SYNONYMS)
    assert not m.stage_agrees_synonym("Discovery call", "Closed", SYNONYMS)
    assert not m.stage_agrees_synonym("something else", "Closed", SYNONYMS)


def test_stage_in_force_is_the_latest_event_at_or_before_the_time() -> None:
    events = [("2024-03-01T00:00:00Z", "Quote"), ("2024-01-01T00:00:00Z", "Discovery"), ("2024-05-01T00:00:00Z", "Closed")]
    assert m.stage_in_force(events, "2023-12-31T00:00:00Z") is None
    assert m.stage_in_force(events, "2024-03-01T00:00:00Z") == "Quote"
    assert m.stage_in_force(events, "2024-04-30T23:59:59Z") == "Quote"
    assert m.stage_in_force(events, "2025-01-01T00:00:00Z") == "Closed"


def test_chance_match_rate_excludes_the_own_deal() -> None:
    deals = {"a": [100.0], "b": [100.5], "c": [5000.0], "d": []}
    assert m.chance_match_rate([100.0], deals, own_deal="a") == pytest.approx(1 / 3)  # only b matches at 1%
    assert m.chance_match_rate([100.0], {"a": [100.0]}, own_deal="a") == 0.0


def test_claim_flags_only_judge_what_the_row_carries() -> None:
    row = {"stage_claim": "Contract Signed", "crm_stage_final": "Closed", "crm_stage_in_force": "Negotiation", "value": "x",
           "stated_amounts": [100.0], "crm_deal_id": "d1", "crm_amount": 100.0}
    flags = m.claim_flags(row, SYNONYMS, {"d1": [100.0, 7.0]})
    assert flags == {"stage_final_strict": False, "stage_final_synonyms": True, "stage_in_force_strict": False,
                     "stage_in_force_synonyms": False, "amount_equals_deal_amount": True, "amount_in_deal_values": True}
    assert m.claim_flags({"field": "summary", "value": "hi"}, SYNONYMS, {}) == {}


def _write_gz(path: Path, rows: list[dict]) -> None:
    with gzip.open(path, "wt", encoding="utf-8") as fh:
        for r in rows:
            fh.write(json.dumps(r) + "\n")


def _flagged(row: dict, deals: dict) -> dict:
    return {**row, **{f"ok_{k}": v for k, v in m.claim_flags(row, SYNONYMS, deals["values"]).items()}}


def _bundle(tmp_path: Path) -> Path:
    deals = {"values": {"d1": [100.0, 7.0], "d2": [100.5], "d3": [900.0]}, "amount": {"d1": 100.0, "d2": 100.5, "d3": 900.0}}
    base = {"account_id": "a1", "deal_id": "u1", "crm_deal_id": "d1", "quote_verbatim": True, "subject_resolved": None,
            "stated_amounts": [], "crm_stage_final": "Closed"}
    pricing = _flagged({**base, "claim_id": "c1", "activity_id": "e1", "field": "commercial_issue", "value": "discount on a $100 item",
                        "speaker_resolved": True, "stated_amounts": [100.0], "crm_amount": 100.0}, deals)
    stage = _flagged({**base, "claim_id": "c2", "activity_id": "e2", "field": "stage", "value": "Contract Signed",
                      "speaker_resolved": False, "stage_claim": "Contract Signed", "crm_stage_in_force": "Negotiation"}, deals)
    events = [{"activity_id": "e1", "type": "EmailSent", "account_id": "a1", "extracted": True, "candidates": 2, "worker_dropped": 1,
               "rejected": 0, "stored": 1, "core_drop_causes": {"core_validation_or_duplicate": 1}},
              {"activity_id": "e2", "type": "EmailSent", "account_id": "a1", "extracted": True, "candidates": 1, "worker_dropped": 0,
               "rejected": 0, "stored": 1, "core_drop_causes": {}},
              {"activity_id": "e3", "type": "EmailReply", "account_id": "a1", "extracted": False}]
    fields = {n: [n == "stage", "crm_explicit" if n == "stage" else None] for n in rc.STATE_FIELDS}
    states = [{"account_id": "a1", "deal_id": "u1", "fields": fields, "state_stage": "Closed",
               "crm_stage_of_pointed_deal": "Closed", "stage_winner_same_deal": True}]
    log = {"latency_s": [1.0, 3.0], "prompt_tokens": 10, "completion_tokens": 5, "cached_tokens": 0, "reported_cost_usd": 0.5,
           "events": {"extract_complete": 2, "hung_retry": 1}, "error_types": {}, "models": {"m": 2}}
    facts = {"dataset": {"name": "n", "licence": "l", "exported_at": "t"}, "activities_by_type": {"EmailSent": 2, "EmailReply": 1},
             "models": {"m@extract-v4": 2}, "quarantined": [], "parked_jobs": 0, "pending_jobs": 0, "unresolved_activities": 0,
             "deals_per_account": {"mean": 3.0, "max": 3}, "claims_by_extractor_standing": [["llm", "first_party_ai", 2]],
             "claims_by_status": {"active": 2}, "email_participants": {"from": {"rows": 3, "with_person_id": 3}}, "notes": ["n1"],
             "worker": {"log": log, "core_warnings_and_errors": {}, "prices_usd_per_token": {"prompt": 1e-6, "completion": 1e-6},
                        "key_meter": {"before": 1.0, "after": 3.0}}}
    out = tmp_path / "bundle"
    out.mkdir()
    (out / "facts.json").write_text(json.dumps(facts), encoding="utf-8")
    (out / "deal_values.json").write_text(json.dumps(deals), encoding="utf-8")
    _write_gz(out / "claims.jsonl.gz", [pricing, stage])
    _write_gz(out / "events.jsonl.gz", events)
    _write_gz(out / "states.jsonl.gz", states)
    return out


def test_recompute_builds_every_section_from_the_bundle_alone(tmp_path: Path) -> None:
    report = rc.build(_bundle(tmp_path), None)
    assert report["run"]["extracted_events"] == 2 and report["run"]["emails_never_extracted"] == 1
    ex = report["extraction"]
    assert (ex["claims_stored"], ex["candidates_returned_by_worker"], ex["worker_dropped_unverifiable_quote"]) == (2, 3, 1)
    assert ex["core_drop_causes"] == {"core_validation_or_duplicate": 1} and ex["stored_quote_pass_rate"] == 1.0
    stage = report["ground_truth"]["stage"]
    assert stage["final_stage_strict"]["agree"] == 0 and stage["final_stage_with_synonyms"]["agree"] == 1
    assert stage["in_force_stage_with_synonyms"]["agree"] == 0 and stage["in_force_stage_with_synonyms"]["compared"] == 1
    amount = report["ground_truth"]["amount_consistency"]
    assert amount["all_amount_bearing_fields"]["emails"] == 1 and amount["all_amount_bearing_fields"]["in_any_deal_value"] == 1
    assert amount["all_amount_bearing_fields"]["chance_in_any_deal_value_rate"] == 0.5  # d2 matches 100 at 1%, d3 does not
    assert report["worker"]["cost_usd"]["attributable_estimate_incl_hung_attempts"] == 0.75
    assert report["account_state_fill"]["fields"]["stage"] == {"known": 1, "unknown": 0, "by_standing": {"crm_explicit": 1}}
    assert "pointer consistency" in report["ground_truth"]["state_level"]["label"].lower()


def test_amount_counts_each_email_once(tmp_path: Path) -> None:
    bundle = _bundle(tmp_path)
    claims = rc.read_jsonl(bundle / "claims.jsonl.gz")
    twin = {**claims[0], "claim_id": "c1b", "field": "product_use_case"}  # same email, second field with the same amount
    _write_gz(bundle / "claims.jsonl.gz", [*claims, twin])
    amount = rc.build(bundle, None)["ground_truth"]["amount_consistency"]
    assert amount["stated_amount_claims"] == 2 and amount["all_amount_bearing_fields"]["emails"] == 1
    assert set(amount["per_field"]) == {"commercial_issue", "product_use_case"}


def test_recompute_check_detects_tampered_flags_and_report_drift(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    bundle = _bundle(tmp_path)
    out = tmp_path / "report"
    argv = ["x", "--bundle", str(bundle), "--out", str(out)]
    monkeypatch.setattr(sys, "argv", argv)
    rc.main()
    monkeypatch.setattr(sys, "argv", [*argv, "--check"])
    with pytest.raises(SystemExit) as ok:
        rc.main()
    assert ok.value.code == 0
    out.with_suffix(".md").write_text("edited", encoding="utf-8")
    with pytest.raises(SystemExit) as drift:
        rc.main()
    assert drift.value.code == 1
    claims = rc.read_jsonl(bundle / "claims.jsonl.gz")
    claims[1]["ok_stage_final_synonyms"] = False
    _write_gz(bundle / "claims.jsonl.gz", claims)
    with pytest.raises(SystemExit) as bad:
        rc.build(bundle, None)
    assert "differ from the recomputed" in str(bad.value)


def test_the_committed_report_is_exactly_what_the_committed_bundle_recomputes() -> None:
    reports = ROOT / "bench" / "reports"
    bundle = reports / "crmarena-extraction-2026-10-03-data"
    report = rc.build(bundle, reports / "crmarena-extraction-cassettes-2026-10-03.json")
    committed = json.loads((reports / "crmarena-extraction-2026-10-03.json").read_text(encoding="utf-8"))
    assert json.loads(json.dumps(report, sort_keys=True, default=str)) == committed
    assert (reports / "crmarena-extraction-2026-10-03.md").read_text(encoding="utf-8") == rc.render_markdown(report)
