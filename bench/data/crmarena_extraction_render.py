"""Markdown rendering of the CRMArena extraction report (see crmarena_extraction_recompute.py)."""
from __future__ import annotations

from typing import Any


def pct(x: float | None) -> str:
    return "n/a" if x is None else f"{x * 100:.1f}%"


def num(x: Any, digits: int = 2) -> str:
    return "n/a" if x is None else f"{x:.{digits}f}" if isinstance(x, float) else str(x)


def table(header: list[str], rows: list[list[Any]]) -> str:
    lines = ["| " + " | ".join(header) + " |", "|" + "|".join("---" for _ in header) + "|"]
    lines += ["| " + " | ".join(str(c) for c in row) + " |" for row in rows]
    return "\n".join(lines)


def rate_row(name: str, a: dict) -> list[Any]:
    return [name, a["compared"], a["agree"], a["disagree"], pct(a["agreement_rate"])]


def run_section(r: dict) -> list[str]:
    run, ex = r["run"], r["extraction"]
    cpe = ex["claims_per_event"]
    return [
        "## Run", "",
        f"- Activities: {run['activities']}; extractable (email with a body): {run['extractable_emails']}; sent to the model and answered: "
        f"**{run['extracted_events']}** ({run['emails_never_extracted']} never answered).",
        f"- Model@extractor version: {run['models']}.",
        f"- Account states: {run['account_states']}; parked jobs: {run['parked_jobs']}; pending jobs: {run['pending_jobs']}; "
        f"unresolved activities: {run['unresolved_activities']}; quarantined: {run['quarantined'] or 'none'}.",
        f"- Cassettes: {r['cassettes']}.", "",
        table(["activity type", "count"], [[k, v] for k, v in run["activities_by_type"].items()]), "",
        "## Extraction", "",
        f"- Claims stored from the model: **{ex['claims_stored']}** over {ex['events']} events; per event mean {num(cpe['mean'])}, "
        f"p50 {num(cpe['p50'])}, p95 {num(cpe['p95'])}, max {num(cpe['max'])}; events with zero claims {ex['events_with_zero_claims']} "
        f"({pct(ex['zero_claim_rate'])}).",
        f"- Verbatim-quote pass rate at the worker: {pct(ex['worker_quote_pass_rate'])} "
        f"({ex['candidates_returned_by_worker']} kept, {ex['worker_dropped_unverifiable_quote']} dropped for a quote that is not in the text).",
        f"- Stored claims whose quote is a substring of the activity body: {ex['stored_claims_with_verbatim_quote']}/{ex['claims_stored']} "
        f"({pct(ex['stored_quote_pass_rate'])}).",
        f"- Candidates the worker returned but core did not store: {ex['core_dropped_after_worker']} {ex['core_drop_causes']}; "
        f"buyer-side owner claims rejected by the worker: {ex['worker_rejected_buyer_side_owner']}.", "",
        table(["field_path", "claims"], [[k, v] for k, v in ex["claims_by_field"].items()]), ""]


def resolution_section(r: dict) -> list[str]:
    er = r["entity_resolution"]
    return ["## Entity resolution", "",
            f"- Person-subject claims resolved to a person: {er['person_claims_resolved']}/{er['person_claims']} ({pct(er['subject_resolution_rate'])}); "
            f"distinct unresolved names: {er['unresolved_distinct_people']}; top: {er['unresolved_top']}.",
            f"- Claims with a resolved speaker: {er['speaker_resolved_claims']}/{er['all_ai_claims']}.", "",
            table(["email participant role", "rows", "with person_id", "rate"],
                  [[k, v["rows"], v["with_person_id"], pct(v["rate"])] for k, v in er["email_participants"].items()]), ""]


def worker_section(r: dict) -> list[str]:
    wk = r["worker"]
    cost, lat = wk["cost_usd"], wk["latency_s"]
    return ["## Latency, cost, provider health", "",
            f"- Model call latency (worker side, seconds): p50 {num(lat['p50'])}, p95 {num(lat['p95'])}, max {num(lat['max'])}, n={lat['count']}.",
            f"- Cost: OpenRouter-reported in responses ${num(cost['openrouter_reported_in_responses'], 4)} "
            f"(${num(cost['per_call_reported'], 6)} per call), attributable estimate including hung attempts "
            f"${num(cost['attributable_estimate_incl_hung_attempts'], 4)}, token-price estimate ${num(cost['token_price_estimate'], 4)}. "
            f"The shared key's meter moved ${num(cost['openrouter_meter_delta_not_ours'], 4)} over the same hours; it is NOT our cost (other consumers share the key).",
            f"- Tokens: prompt {wk['log']['prompt_tokens']}, completion {wk['log']['completion_tokens']}, cached {wk['log']['cached_tokens']}.",
            f"- Provider events: {wk['log']['events']}; error types {wk['log']['error_types']}; models answering {wk['log']['models']}.",
            f"- Core warnings/errors: {wk['core_warnings_and_errors'] or 'none'}.", ""]


def stage_section(g: dict) -> list[str]:
    s = g["stage"]
    return ["### Stage (model claims vs the CRM deal's stage)", "",
            f"{s['n_stage_claims']} stage claims. {s['label']}.", "",
            table(["comparison", "compared", "agree", "disagree", "agreement"], [
                rate_row("final CRM stage, strict", s["final_stage_strict"]),
                rate_row("final CRM stage, with synonym map", s["final_stage_with_synonyms"]),
                rate_row("stage in force at send time (derived), strict", s["in_force_stage_strict"]),
                rate_row("stage in force at send time (derived), with synonym map", s["in_force_stage_with_synonyms"]),
                rate_row("final CRM stage, synonym map, same subset as the in-force rows", s["final_stage_with_synonyms_same_subset"])]), "",
            ("" if s["informative"] else
               "**The in-force comparison is NOT INFORMATIVE on this snapshot:** the loader derives one stage event per deal (1170 events for "
               "1170 deals, the final stage), so for almost every email no stage is in force yet and only the rows above with a small n could be judged. "
               "The final-stage rows are the usable ones. ") +
            f"Claims with no stage yet in force when the email was sent: {s['claims_without_a_stage_in_force_at_send_time']}. "
            "The synonym map (`bench/data/crmarena_stage_synonyms.json`) was written after reading the first disagreements, so the "
            "synonym rows are an upper bound and the strict rows the conservative figure.", "",
            f"Remaining disagreements (in force, with synonyms): {s['examples_disagree_after_synonyms_in_force']}", ""]


def amount_section(g: dict) -> list[str]:
    a = g["amount_consistency"]

    def row(name: str, b: dict) -> list[Any]:
        return [name, b["emails"], b["equals_deal_amount"], pct(b["equals_deal_amount_rate"]), pct(b["chance_equals_deal_amount_rate"]),
                b["in_any_deal_value"], pct(b["in_any_deal_value_rate"]), pct(b["chance_in_any_deal_value_rate"])]

    rows = [row("all fields with a stated amount", a["all_amount_bearing_fields"]),
            row(f"pricing field only ({a['pricing_field_only']['field']})", a["pricing_field_only"])]
    rows += [row(f"field {f}", b) for f, b in a["per_field"].items()]
    return ["### Dollar amounts: quote-to-CRM consistency check (not accuracy)", "",
            f"{a['label']}. A match mostly shows the dataset repeating itself, so do not read it as extraction accuracy. "
            "\"Chance\" is the share of the OTHER 1169 deals a random pick would match at 1% (exact, not sampled).", "",
            table(["scope", "emails", "= deal Amount", "rate", "chance", "in any deal value", "rate", "chance"], rows), "",
            f"Examples not in any CRM value of the deal: {a['examples_not_in_any_deal_value']}", ""]


def agreement_section(r: dict) -> list[str]:
    g = r["ground_truth"]
    sl = g["state_level"]
    out = ["## Agreement with the CRM's structured fields", "", "Extraction layer (model claims vs CRM records):", ""]
    out += stage_section(g)
    out += amount_section(g)
    out += [table(["field", "compared", "agree", "disagree", "agreement"],
                  [rate_row("owner", g["owner"]), rate_row("contact title or role (word overlap)", g["contact_title_or_role"])]), "",
            f"- Roles: {g['opportunity_contact_role_ground_truth']}.",
            f"- Owner disagreements: {g['owner']['examples_disagree']}; title disagreements: {g['contact_title_or_role']['examples_disagree']}.", "",
            "State layer:", "", sl["label"] + ".", "",
            table(["check", "compared", "agree", "disagree", "agreement"],
                  [rate_row(k, v) for k, v in sl.items() if isinstance(v, dict) and "compared" in v]), "",
            f"Deals per account: {sl['deals_per_account']}.", ""]
    return out


def render_markdown(r: dict) -> str:
    out = ["# CRMArena-Pro B2B extraction run (HAR-104 WP6, HAR-130)", "",
           f"Dataset: {r['dataset']['name']}, {r['dataset']['licence']}, exported {r['dataset']['exported_at']}. "
           "Third-party synthetic data (Salesforce AI Research); attribution as in docs/data/crmarena-b2b.md.",
           "Every number below is recomputed from the committed bundle by `bench/data/crmarena_extraction_recompute.py`.", ""]
    out += [f"- {n}" for n in r["notes"]] + [""]
    out += run_section(r) + resolution_section(r) + worker_section(r)
    out += ["## AccountState fields: known vs unknown", "", f"{r['account_state_fill']['accounts']} account states.", "",
            table(["field", "known", "unknown", "known by standing"],
                  [[k, v["known"], v["unknown"], v["by_standing"]] for k, v in r["account_state_fill"]["fields"].items()]), ""]
    out += agreement_section(r)
    return "\n".join(out)
