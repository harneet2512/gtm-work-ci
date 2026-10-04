"""Measure what the CRMArena-Pro B2B snapshot has and lacks (HAR-130 data card numbers).

    python bench/data/crmarena_gaps.py [--snapshot data/crmarena_b2b] [--out bench/reports/crmarena-gaps-<date>.json]

Reads the git-ignored export written by crmarena_export.py; prints and writes counts only (no records).
The quote-acceptance AUC needs scikit-learn and is skipped without it.
"""
from __future__ import annotations

import argparse
import json
import math
import sys
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
SELLER_DOMAINS = ("techagents.com", "techdomain.com")  # the seller's user domains in this org
Records = list[dict[str, Any]]


def load(snapshot: Path, name: str) -> Records:
    return json.loads((snapshot / f"{name}.json").read_text(encoding="utf-8"))


def date_range(records: Records, field: str) -> dict[str, Any]:
    values = sorted(str(r[field])[:10] for r in records if r.get(field))
    return {"field": field, "min": values[0] if values else None, "max": values[-1] if values else None, "n": len(values)}


def coverage(records: Records, fields: tuple[str, ...]) -> dict[str, float]:
    n = len(records) or 1
    return {f: round(sum(1 for r in records if r.get(f) not in (None, "", [])) / n, 4) for f in fields}


def activity_dates(d: dict[str, Records]) -> dict[str, list[str]]:
    out: dict[str, list[str]] = defaultdict(list)
    for e in d["EmailMessage"]:
        out[e["RelatedToId"]].append(e["MessageDate"][:10])
    for t in d["Task"]:
        out[t["WhatId"]].append(t["ActivityDate"])
    return out


def people_gaps(d: dict[str, Records]) -> dict[str, Any]:
    roles = Counter(r.get("Role") for r in d["OpportunityContactRole"])
    per_deal = Counter(r["OpportunityId"] for r in d["OpportunityContactRole"])
    users_by_email = Counter((u.get("Email") or "").lower() for u in d["User"])
    return {
        "account_history_fields": dict(Counter(h.get("Field") for h in d["AccountHistory"])),
        "contact_history_fields": dict(Counter(h.get("Field") for h in d["ContactHistory"])),
        "contact_role_values": {str(k): v for k, v in roles.items()},
        "contacts_per_deal": dict(Counter(per_deal.values())),
        "contacts_with_reports_to": sum(1 for c in d["Contact"] if c.get("ReportsToId")),
        "users_with_title_department_manager": sum(1 for u in d["User"] if u.get("Title") or u.get("Department") or u.get("ManagerId")),
        "user_addresses_with_several_user_records": sum(1 for n in users_by_email.values() if n > 1),
    }


def email_gaps(d: dict[str, Records]) -> dict[str, Any]:
    em = d["EmailMessage"]
    customer = [e for e in em if not e["FromAddress"].lower().endswith(SELLER_DOMAINS)]
    return {
        "emails": len(em),
        "incoming_flag_true": sum(1 for e in em if e.get("Incoming")),
        "sent_by_customer_address": len(customer),
        "sent_by_customer_share": round(len(customer) / len(em), 4),
        "re_subjects": sum(1 for e in em if e["Subject"].lower().startswith("re:")),
        "with_reply_to_id": sum(1 for e in em if e.get("ReplyToEmailMessageId")),
        "with_thread_or_message_id": sum(1 for e in em if e.get("ThreadIdentifier") or e.get("MessageIdentifier")),
        "recipients_per_email": dict(Counter(len([a for a in (e.get("ToAddress") or "").split(";") if a.strip()]) for e in em)),
        "with_cc_or_bcc": sum(1 for e in em if e.get("CcAddress") or e.get("BccAddress")),
        "related_to_prefix": dict(Counter((e.get("RelatedToId") or "")[:3] for e in em)),
    }


def deal_gaps(d: dict[str, Records]) -> dict[str, Any]:
    opp, hist = d["Opportunity"], d["OpportunityHistory"]
    stages: dict[str, set[str]] = defaultdict(set)
    for h in hist:
        stages[h["OpportunityId"]].add(h["StageName"])
    acts = activity_dates(d)
    created_after_activity = sum(1 for o in opp if acts.get(o["Id"]) and o["CreatedDate"][:10] > min(acts[o["Id"]]))
    return {
        "stage_names": dict(Counter(o["StageName"] for o in opp)),
        "is_closed_true": sum(1 for o in opp if o.get("IsClosed")),
        "is_won_true": sum(1 for o in opp if o.get("IsWon")),
        "stage_history_rows": len(hist),
        "stage_history_created": date_range(hist, "CreatedDate"),
        "deals_with_more_than_one_stage_in_history": sum(1 for s in stages.values() if len(s) > 1),
        "deals_without_email_or_task": sum(1 for o in opp if o["Id"] not in acts),
        "deals_created_after_first_activity": created_after_activity,
        "deals_closedate_before_last_activity": sum(1 for o in opp if acts.get(o["Id"]) and o["CloseDate"] < max(acts[o["Id"]])),
    }


def quote_gaps(d: dict[str, Records]) -> dict[str, Any]:
    acts = activity_dates(d)
    with_acts = [q for q in d["Quote"] if acts.get(q["OpportunityId"])]
    before = sum(1 for q in with_acts if q["CreatedDate"][:10] < min(acts[q["OpportunityId"]]))
    return {"status": dict(Counter(q["Status"] for q in d["Quote"])), "quotes_on_deals_with_activity": len(with_acts),
            "created_before_first_deal_activity": before, "acceptance_auc": acceptance_auc(d)}


def acceptance_auc(d: dict[str, Records]) -> dict[str, Any] | None:
    """Account-grouped 5-fold logistic regression: Accepted vs every other status, from quote fields."""
    try:
        import numpy as np
        from sklearn.linear_model import LogisticRegression
        from sklearn.metrics import roc_auc_score
        from sklearn.model_selection import GroupKFold, cross_val_predict
    except ImportError:
        return None
    q = [r for r in d["Quote"] if r.get("GrandTotal") is not None]
    x = np.array([[math.log1p(r["GrandTotal"]), r.get("Discount") or 0.0, r.get("LineItemCount") or 0,
                   (_days(r["ExpirationDate"]) - _days(r["CreatedDate"][:10]))] for r in q])
    y = np.array([r["Status"] == "Accepted" for r in q])
    groups = np.array([r["AccountId"] for r in q])
    x = (x - x.mean(axis=0)) / (x.std(axis=0) + 1e-9)
    p = cross_val_predict(LogisticRegression(max_iter=1000), x, y, groups=groups, cv=GroupKFold(5), method="predict_proba")[:, 1]
    return {"auc": round(float(roc_auc_score(y, p)), 3), "quotes": len(q), "positives": int(y.sum()),
            "features": ["log GrandTotal", "Discount", "LineItemCount", "validity days"], "cv": "GroupKFold(5) by AccountId"}


def _days(day: str) -> int:
    from datetime import date
    return date.fromisoformat(day).toordinal()


def link_gaps(d: dict[str, Records]) -> dict[str, Any]:
    t, ch, c, o = d["Task"], d["LiveChatTranscript"], d["Contract"], d["Order"]
    return {
        "tasks_with_who": sum(1 for r in t if r.get("WhoId")), "task_types": dict(Counter(r.get("Type") for r in t)),
        "task_status": dict(Counter(r.get("Status") for r in t)), "tasks_completed_at": sum(1 for r in t if r.get("CompletedDateTime")),
        "contracts_with_source_opportunity": sum(1 for r in c if r.get("SourceOpportunityId")),
        "deals_naming_a_contract": sum(1 for r in d["Opportunity"] if r.get("ContractId__c")),
        "orders_with_contract": sum(1 for r in o if r.get("ContractId")),
        "chats_with_contact": sum(1 for r in ch if r.get("ContactId")), "chats_with_start_time": sum(1 for r in ch if r.get("StartTime")),
        "accounts_with_website": sum(1 for r in d["Account"] if r.get("Website")),
        "converted_leads": sum(1 for r in d["Lead"] if r.get("Status") == "Converted"),
        "leads_with_converted_opportunity": sum(1 for r in d["Lead"] if r.get("ConvertedOpportunityId")),
    }


DATES = {"Account": "CreatedDate", "Contact": "CreatedDate", "Opportunity": "CreatedDate", "EmailMessage": "MessageDate",
         "Task": "ActivityDate", "Quote": "CreatedDate", "Order": "EffectiveDate", "Contract": "CustomerSignedDate",
         "Case": "CreatedDate", "LiveChatTranscript": "EndTime", "OpportunityHistory": "CreatedDate"}
COVERAGE = {"Contact": ("Email", "Title", "Department", "Phone", "ReportsToId"),
            "Opportunity": ("Amount", "CloseDate", "Description", "NextStep", "Type", "LeadSource", "ContractId__c"),
            "EmailMessage": ("TextBody", "FromName", "CcAddress", "ThreadIdentifier", "ReplyToEmailMessageId"),
            "Task": ("Description", "WhoId", "CompletedDateTime"), "Quote": ("GrandTotal", "Discount", "Description", "ContactId"),
            "Case": ("ClosedDate", "ContactId", "Description"), "Account": ("Website", "Industry", "NumberOfEmployees", "Type")}


def measure(snapshot: Path) -> dict[str, Any]:
    names = set(DATES) | set(COVERAGE) | {"User", "OpportunityContactRole", "AccountHistory", "ContactHistory", "Lead"}
    d = {n: load(snapshot, n) for n in sorted(names)}
    manifest = json.loads((snapshot / "manifest.json").read_text(encoding="utf-8"))
    return {"snapshot_exported_at": manifest["exported_at"], "counts": manifest["counts"],
            "date_ranges": {n: date_range(d[n], f) for n, f in DATES.items()},
            "coverage": {n: coverage(d[n], f) for n, f in COVERAGE.items()},
            "people": people_gaps(d), "email": email_gaps(d), "deals": deal_gaps(d), "quotes": quote_gaps(d), "links": link_gaps(d)}


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--snapshot", type=Path, default=ROOT / "data" / "crmarena_b2b")
    ap.add_argument("--out", type=Path)
    args = ap.parse_args(argv)
    text = json.dumps(measure(args.snapshot), indent=2, sort_keys=True) + "\n"
    if args.out:
        args.out.write_bytes(text.encode("utf-8"))
    print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
