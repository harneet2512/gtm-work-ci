"""Recompute every table of the CRMArena extraction report from the committed bundle alone (HAR-104 / HAR-130).

    python bench/data/crmarena_extraction_recompute.py --bundle bench/reports/crmarena-extraction-2026-10-03-data \\
        --out bench/reports/crmarena-extraction-2026-10-03 [--check]

No database, no network, no CRMArena export: only the bundle (facts.json, events/claims/states .jsonl.gz, deal_values.json), the stage
synonym map and the cassette manifest, all committed. --check recomputes and fails when the written report differs, or when the agreement
flags stored in claims.jsonl.gz differ from the flags recomputed from the CRM values the same rows carry.
"""
from __future__ import annotations

import argparse
import gzip
import json
import sys
from collections import Counter
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import crmarena_extraction_metrics as m  # noqa: E402
from crmarena_extraction_render import render_markdown  # noqa: E402

HERE = Path(__file__).resolve().parent
SYNONYMS = HERE / "crmarena_stage_synonyms.json"
STATE_FIELDS = ["stage", "health", "owner", "motion", "champion", "champion_status", "economic_buyer", "blockers",
                "objections", "decision_criteria", "decision_process", "current_commitments", "next_milestone",
                "next_meeting", "relationship_risk", "product_use_case", "commercial_issue",
                "last_customer_interaction", "last_meaningful_change", "summary"]
AMOUNT_BEARING_FIELD = "commercial_issue"  # the pricing field; the others are reported per field
EXAMPLES = 6
MIN_INFORMATIVE = 10  # fewer comparisons than this say nothing


def read_jsonl(path: Path) -> list[dict]:
    with gzip.open(path, "rt", encoding="utf-8") as fh:
        return [json.loads(line) for line in fh]


def rate_block(flags: list[bool]) -> dict:
    n, ok = len(flags), sum(flags)
    return {"compared": n, "agree": ok, "disagree": n - ok, "agreement_rate": m.rate(ok, n)}


def stored_flags(row: dict) -> dict[str, bool]:
    return {k[3:]: v for k, v in row.items() if k.startswith("ok_")}


def check_flags(claims: list[dict], synonyms: dict, deals: dict) -> int:
    """Rows whose stored flags differ from the flags recomputed from their own CRM values."""
    return sum(1 for r in claims if stored_flags(r) != m.claim_flags(r, synonyms, deals["values"]))


def extraction(events: list[dict], claims: list[dict], facts: dict) -> dict:
    done = [e for e in events if e["extracted"]]
    per_event = [e["stored"] for e in done]
    zero = sum(1 for v in per_event if v == 0)
    by_type: dict[str, list[int]] = {}
    for e in done:
        by_type.setdefault(e["type"], []).append(e["stored"])
    candidates = sum(e["candidates"] for e in done)
    dropped = sum(e["worker_dropped"] for e in done)
    causes: Counter[str] = Counter()
    for e in done:
        causes.update(e["core_drop_causes"])
    verbatim = sum(1 for c in claims if c["quote_verbatim"])
    return {
        "events": len(done), "claims_stored": len(claims), "claims_per_event": m.summarize(per_event),
        "events_with_zero_claims": zero, "zero_claim_rate": m.rate(zero, len(done)),
        "claims_per_event_by_type": {k: m.summarize(v) for k, v in sorted(by_type.items())},
        "candidates_returned_by_worker": candidates, "worker_dropped_unverifiable_quote": dropped,
        "worker_rejected_buyer_side_owner": sum(e["rejected"] for e in done),
        "worker_quote_pass_rate": m.rate(candidates, candidates + dropped),
        "core_dropped_after_worker": sum(causes.values()), "core_drop_causes": dict(causes),
        "stored_claims_with_verbatim_quote": verbatim, "stored_quote_pass_rate": m.rate(verbatim, len(claims)),
        "claims_by_field": dict(Counter(c["field"] for c in claims).most_common()),
        "claims_by_extractor_standing": facts["claims_by_extractor_standing"], "claims_by_status": facts["claims_by_status"]}


def resolution(claims: list[dict], facts: dict) -> dict:
    person = [c for c in claims if c["subject_resolved"] is not None]
    hit = sum(1 for c in person if c["subject_resolved"])
    by_field: dict[str, list[bool]] = {}
    for c in person:
        by_field.setdefault(c["field"], []).append(c["subject_resolved"])
    unresolved = Counter(c["value"].lower() for c in person if not c["subject_resolved"])
    speakers = sum(1 for c in claims if c["speaker_resolved"])
    return {"person_claims": len(person), "person_claims_resolved": hit, "subject_resolution_rate": m.rate(hit, len(person)),
            "by_field": {f: {"claims": len(v), "resolved": sum(v), "rate": m.rate(sum(v), len(v))} for f, v in sorted(by_field.items())},
            "unresolved_distinct_people": len(unresolved), "unresolved_top": unresolved.most_common(15),
            "speaker_resolved_claims": speakers, "all_ai_claims": len(claims),
            "email_participants": {k: {**v, "rate": m.rate(v["with_person_id"], v["rows"])} for k, v in facts["email_participants"].items()}}


def stage_block(claims: list[dict]) -> dict:
    rows = [c for c in claims if "stage_claim" in c]
    in_force = [c for c in rows if c.get("crm_stage_in_force")]
    both = {
        "n_stage_claims": len(rows),
        "final_stage_strict": rate_block([c["ok_stage_final_strict"] for c in rows]),
        "final_stage_with_synonyms": rate_block([c["ok_stage_final_synonyms"] for c in rows]),
        "claims_without_a_stage_in_force_at_send_time": len(rows) - len(in_force),
        "in_force_stage_strict": rate_block([c["ok_stage_in_force_strict"] for c in in_force]),
        "in_force_stage_with_synonyms": rate_block([c["ok_stage_in_force_synonyms"] for c in in_force]),
        "final_stage_with_synonyms_same_subset": rate_block([c["ok_stage_final_synonyms"] for c in in_force]),
        "label": "in-force stage = the loader's DERIVED stage events (the snapshot has no real stage history; OpportunityHistory rows carry load dates)",
        "informative": len(in_force) >= MIN_INFORMATIVE,
    }
    both["examples_disagree_after_synonyms_in_force"] = [
        {"claimed": c["stage_claim"], "crm_in_force": c["crm_stage_in_force"], "crm_final": c["crm_stage_final"]}
        for c in in_force if not c["ok_stage_in_force_synonyms"]][:EXAMPLES]
    return both


def amount_block(claims: list[dict], deals: dict) -> dict:
    """A quote<->CRM consistency check, NOT accuracy: CRMArena emails were generated from CRM fields, so a match mostly reflects the
    dataset repeating itself. Each email counts once; reported per field, restricted to the pricing field, and against a chance baseline."""
    stated = [c for c in claims if c.get("stated_amounts") and c.get("crm_deal_id")]
    by_email: dict[str, list[dict]] = {}
    for c in stated:
        by_email.setdefault(c["activity_id"], []).append(c)

    def per_email(rows_of: dict[str, list[dict]]) -> dict:
        eq = [any(r["ok_amount_equals_deal_amount"] for r in rs) for rs in rows_of.values()]
        inv = [any(r["ok_amount_in_deal_values"] for r in rs) for rs in rows_of.values()]
        chance_in, chance_eq = [], []
        for rs in rows_of.values():
            amounts = sorted({a for r in rs for a in r["stated_amounts"]})
            own = rs[0]["crm_deal_id"]
            chance_in.append(m.chance_match_rate(amounts, deals["values"], own))
            chance_eq.append(m.chance_match_rate(amounts, {k: [v] if v else [] for k, v in deals["amount"].items()}, own))
        n = len(rows_of)
        return {"emails": n, "equals_deal_amount": sum(eq), "equals_deal_amount_rate": m.rate(sum(eq), n),
                "in_any_deal_value": sum(inv), "in_any_deal_value_rate": m.rate(sum(inv), n),
                "chance_equals_deal_amount_rate": round(sum(chance_eq) / n, 4) if n else None,
                "chance_in_any_deal_value_rate": round(sum(chance_in) / n, 4) if n else None}

    fields: dict[str, dict[str, list[dict]]] = {}
    for c in stated:
        fields.setdefault(c["field"], {}).setdefault(c["activity_id"], []).append(c)
    return {
        "label": "quote<->CRM consistency check, not accuracy (the emails were generated from CRM fields); one email = one observation",
        "stated_amount_claims": len(stated),
        "all_amount_bearing_fields": per_email(by_email),
        "pricing_field_only": {"field": AMOUNT_BEARING_FIELD, **per_email(fields.get(AMOUNT_BEARING_FIELD, {}))},
        "per_field": {f: per_email(v) for f, v in sorted(fields.items())},
        "examples_not_in_any_deal_value": [
            {"claimed": r["stated_amounts"][:3], "crm_amount": r.get("crm_amount"), "field": r["field"]}
            for rs in by_email.values() for r in rs[:1] if not r["ok_amount_in_deal_values"]][:EXAMPLES],
    }


def state_ground_truth(states: list[dict], deals_per_account: dict) -> dict:
    stage = [s for s in states if "state_stage" in s]
    owner = [s for s in states if "state_owner" in s]
    return {
        "label": "POINTER CONSISTENCY, not quality: the state's stage/owner are CRM claims (crm_explicit) compared with the CRM deal the state points at; it checks the CRM against itself",
        "stage_vs_deal_the_state_points_at": rate_block([m.stage_agrees(s["state_stage"], s["crm_stage_of_pointed_deal"]) for s in stage]),
        "owner_vs_deal_the_state_points_at": rate_block([m.names_agree(s["state_owner"], s["crm_owner_of_pointed_deal"]) for s in owner]),
        "stage_winner_comes_from_the_state_deal": rate_block([s["stage_winner_same_deal"] for s in stage]),
        "deals_per_account": deals_per_account}


def build(bundle: Path, cassette_manifest: Path | None) -> dict:
    facts = json.loads((bundle / "facts.json").read_text(encoding="utf-8"))
    deals = json.loads((bundle / "deal_values.json").read_text(encoding="utf-8"))
    claims, events, states = (read_jsonl(bundle / f"{n}.jsonl.gz") for n in ("claims", "events", "states"))
    synonyms = json.loads(SYNONYMS.read_text(encoding="utf-8"))
    mismatched = check_flags(claims, synonyms, deals)
    if mismatched:
        raise SystemExit(f"{mismatched} claim rows carry agreement flags that differ from the recomputed ones")
    extracted = sum(1 for e in events if e["extracted"])
    run = {"activities": sum(facts["activities_by_type"].values()), "activities_by_type": facts["activities_by_type"],
           "extractable_emails": len(events), "extracted_events": extracted, "models": facts["models"],
           "quarantined": facts["quarantined"], "parked_jobs": facts["parked_jobs"], "pending_jobs": facts["pending_jobs"],
           "account_states": len(states), "unresolved_activities": facts["unresolved_activities"],
           "emails_never_extracted": len(events) - extracted}
    owner = [c for c in claims if "owner_claimed" in c]
    title = [c for c in claims if c.get("crm_title")]
    gt = {"stage": stage_block(claims), "amount_consistency": amount_block(claims, deals),
          "owner": {**rate_block([c["ok_owner"] for c in owner]), "examples_disagree": [
              {"claimed": c["owner_claimed"], "crm": c["crm_owner"]} for c in owner if not c["ok_owner"]][:EXAMPLES]},
          "contact_title_or_role": {**rate_block([c["ok_title_overlap"] for c in title]), "examples_disagree": [
              {"claimed": c["value"], "crm_title": c["crm_title"]} for c in title if not c["ok_title_overlap"]][:EXAMPLES]},
          "opportunity_contact_role_ground_truth": "none: OpportunityContactRole.Role is empty on every row of the snapshot",
          "state_level": state_ground_truth(states, facts["deals_per_account"])}
    worker = worker_section(facts["worker"], extracted)
    fill = m.fill_table([{"fields": {n: {"known": k, "standing": st} for n, (k, st) in s["fields"].items()}} for s in states], STATE_FIELDS)
    manifest = json.loads(cassette_manifest.read_text(encoding="utf-8")) if cassette_manifest and cassette_manifest.exists() else None
    return {"dataset": facts["dataset"], "run": run, "extraction": extraction(events, claims, facts),
            "entity_resolution": resolution(claims, facts),
            "account_state_fill": {"accounts": len(states), "fields": fill}, "ground_truth": gt, "worker": worker,
            "cassettes": manifest and {k: manifest[k] for k in ("files", "total_bytes", "aggregate_sha256")}, "notes": facts["notes"]}


def worker_section(w: dict, extracted: int) -> dict:
    log = dict(w["log"])
    lat = log.pop("latency_s")
    calls = log["events"].get("extract_complete", 0)
    price = w["prices_usd_per_token"]
    est = m.token_cost(log["prompt_tokens"], log["completion_tokens"], price["prompt"], price["completion"])
    hung = log["events"].get("hung_retry", 0)
    reported = log["reported_cost_usd"]
    attributable = reported + (hung * reported / calls if calls else 0)
    meter = w["key_meter"]
    delta = None if meter["before"] is None or meter["after"] is None else round(meter["after"] - meter["before"], 4)
    return {"latency_s": m.summarize(lat), "successful_calls": calls, "log": log,
            "cost_usd": {"openrouter_reported_in_responses": reported, "token_price_estimate": round(est, 4),
                         "attributable_estimate_incl_hung_attempts": round(attributable, 4),
                         "openrouter_meter_delta_not_ours": delta,
                         "per_call_reported": round(reported / calls, 6) if calls else None},
            "core_warnings_and_errors": w["core_warnings_and_errors"]}


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bundle", type=Path, required=True)
    ap.add_argument("--cassette-manifest", type=Path)
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--check", action="store_true", help="fail if the written report differs from the recomputed one")
    args = ap.parse_args()
    report = build(args.bundle, args.cassette_manifest)
    js = json.dumps(report, indent=1, sort_keys=True, default=str) + "\n"
    md = render_markdown(report)
    jpath, mpath = args.out.with_suffix(".json"), args.out.with_suffix(".md")
    if args.check:
        same = jpath.read_text(encoding="utf-8") == js and mpath.read_text(encoding="utf-8") == md
        print("report matches the bundle" if same else "REPORT DIFFERS from the bundle")
        sys.exit(0 if same else 1)
    jpath.write_text(js, encoding="utf-8", newline="\n")
    mpath.write_text(md, encoding="utf-8", newline="\n")
    print("wrote", jpath, mpath)


if __name__ == "__main__":
    main()
