"""Cut the small, sanitized CRMArena-Pro B2B sample committed under fixtures/crmarena_sample/ (HAR-130).

    python bench/data/crmarena_sample.py [--snapshot data/crmarena_b2b] [--out fixtures/crmarena_sample]

Picks, deterministically, the three smallest accounts that have every record kind the loader maps
(cases with a chat transcript, a contract, an order, quotes) and deals on both sides of SAMPLE_CUTOFF,
then writes every record of those accounts restricted to the fields the Go loader reads (FIELDS).
Org-internal data (login usernames, admin/system users, view tracking) never leaves data/.

The records are third-party synthetic data by Salesforce AI Research, licence CC BY-NC 4.0
(non-commercial use only); see fixtures/crmarena_sample/README.md.
"""
from __future__ import annotations

import argparse
import json
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
SAMPLE_CUTOFF = "2023-11-01"  # the frozen cutoff of bench/data/deal_split.json; keep in sync
ACCOUNTS = 3

# Fields the Go loader (core-go/internal/crmarena) reads, per sObject.
FIELDS: dict[str, tuple[str, ...]] = {
    "Account": ("Id", "Name", "Industry", "NumberOfEmployees", "Description"),
    "Contact": ("Id", "AccountId", "FirstName", "LastName", "Email", "Title", "Department"),
    "User": ("Id", "Name", "Email", "UserType"),
    "Opportunity": ("Id", "AccountId", "Name", "Description", "StageName", "Amount", "CloseDate", "OwnerId",
                    "CreatedDate", "ContractId__c"),
    "EmailMessage": ("Id", "RelatedToId", "FromAddress", "FromName", "ToAddress", "CcAddress", "Subject",
                     "TextBody", "MessageDate"),
    "Task": ("Id", "WhatId", "WhoId", "AccountId", "OwnerId", "Subject", "Description", "ActivityDate", "Priority", "Type"),
    "Quote": ("Id", "OpportunityId", "AccountId", "Name", "QuoteNumber", "Status", "CreatedDate", "ExpirationDate",
              "GrandTotal", "Discount", "Description", "ContactId"),
    "Order": ("Id", "AccountId", "OwnerId", "EffectiveDate", "Status", "OrderNumber", "BillToContactId", "ShipToContactId"),
    "Contract": ("Id", "AccountId", "ContractNumber", "StartDate", "EndDate", "ContractTerm", "CustomerSignedDate",
                 "CompanySignedDate", "Status", "Description"),
    "Case": ("Id", "AccountId", "ContactId", "OwnerId", "CaseNumber", "Subject", "Description", "Priority", "Origin",
             "CreatedDate", "ClosedDate"),
    "LiveChatTranscript": ("Id", "CaseId", "AccountId", "OwnerId", "Body", "EndTime"),
    "OpportunityContactRole": ("Id", "ContactId", "OpportunityId"),
}


def load(snapshot: Path, name: str) -> list[dict[str, Any]]:
    return json.loads((snapshot / f"{name}.json").read_text(encoding="utf-8"))


def deal_windows(data: dict[str, list[dict[str, Any]]]) -> dict[str, tuple[str, str]]:
    """Opportunity id -> (first, last) event date: emails, tasks, quotes and the contract signature.

    Mirrors the Go loader (core-go/internal/crmarena Windows): a deal has ended at its last event of
    any kind, not at its last email or task.
    """
    dates: dict[str, list[str]] = defaultdict(list)
    for e in data["EmailMessage"]:
        dates[e["RelatedToId"]].append(e["MessageDate"][:10])
    for t in data["Task"]:
        dates[t["WhatId"]].append(t["ActivityDate"])
    for q in data["Quote"]:
        if q.get("OpportunityId"):
            dates[q["OpportunityId"]].append(q["CreatedDate"][:10])
    contracts = {c["Id"]: c for c in data["Contract"]}
    for o in data["Opportunity"]:
        c = contracts.get(o.get("ContractId__c") or "")
        if c:
            signed = [d for d in (c.get("CustomerSignedDate"), c.get("CompanySignedDate")) if d]
            dates[o["Id"]].append(max(signed) if signed else c["StartDate"])
    return {k: (min(v), max(v)) for k, v in dates.items()}


def qualifies(acct: str, data: dict[str, list[dict[str, Any]]], windows: dict[str, tuple[str, str]]) -> bool:
    def has(name: str) -> bool:
        return any(r.get("AccountId") == acct for r in data[name])
    opps = [o["Id"] for o in data["Opportunity"] if o["AccountId"] == acct and o["Id"] in windows]
    previous = any(windows[o][1] < SAMPLE_CUTOFF for o in opps)
    current = any(windows[o][0] < SAMPLE_CUTOFF <= windows[o][1] for o in opps)
    return all(has(n) for n in ("Case", "LiveChatTranscript", "Contract", "Order", "Quote")) and previous and current


def pick_accounts(data: dict[str, list[dict[str, Any]]]) -> list[str]:
    windows = deal_windows(data)
    size = defaultdict(int)
    for name in ("Opportunity", "Task", "Contact"):
        for r in data[name]:
            size[r.get("AccountId")] += 1
    candidates = [a["Id"] for a in data["Account"] if qualifies(a["Id"], data, windows)]
    return sorted(candidates, key=lambda a: (size[a], a))[:ACCOUNTS]


def restrict(records: list[dict[str, Any]], fields: tuple[str, ...]) -> list[dict[str, Any]]:
    return sorted(({f: r.get(f) for f in fields} for r in records), key=lambda r: r["Id"])


def cut(data: dict[str, list[dict[str, Any]]], accounts: list[str]) -> dict[str, list[dict[str, Any]]]:
    keep = set(accounts)
    opps = {o["Id"] for o in data["Opportunity"] if o["AccountId"] in keep}
    out = {name: [r for r in data[name] if r.get("AccountId") in keep]
           for name in ("Account", "Contact", "Opportunity", "Task", "Quote", "Order", "Contract", "Case",
                        "LiveChatTranscript")}
    out["Account"] = [a for a in data["Account"] if a["Id"] in keep]
    out["EmailMessage"] = [e for e in data["EmailMessage"] if e["RelatedToId"] in opps]
    out["OpportunityContactRole"] = [r for r in data["OpportunityContactRole"] if r["OpportunityId"] in opps]
    out["User"] = [u for u in data["User"] if u["Id"] in referenced_users(out, data["User"])]
    return {name: restrict(out[name], FIELDS[name]) for name in FIELDS}


def referenced_users(out: dict[str, list[dict[str, Any]]], users: list[dict[str, Any]]) -> set[str]:
    """Owners of loader-mapped records plus users whose address appears on a sample email."""
    ids = {r["OwnerId"] for n in ("Opportunity", "Task", "Order", "Case", "LiveChatTranscript") for r in out[n]}
    addresses = set()
    for e in out["EmailMessage"]:
        addresses.add(e["FromAddress"].lower())
        addresses.update(a.strip().lower() for a in (e.get("ToAddress") or "").split(";") if a.strip())
    ids |= {u["Id"] for u in users if (u.get("Email") or "").lower() in addresses}
    return ids


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--snapshot", type=Path, default=ROOT / "data" / "crmarena_b2b")
    ap.add_argument("--out", type=Path, default=ROOT / "fixtures" / "crmarena_sample")
    args = ap.parse_args(argv)
    data = {name: load(args.snapshot, name) for name in FIELDS}
    accounts = pick_accounts(data)
    if len(accounts) < ACCOUNTS:
        print(f"only {len(accounts)} qualifying accounts", file=sys.stderr)
        return 1
    args.out.mkdir(parents=True, exist_ok=True)
    for name, records in cut(data, accounts).items():
        text = json.dumps(records, indent=2, sort_keys=True, ensure_ascii=False) + "\n"
        (args.out / f"{name}.json").write_bytes(text.encode("utf-8"))
        print(f"{name:20s} {len(records):5d}")
    print("accounts:", ", ".join(accounts))
    return 0


if __name__ == "__main__":
    sys.exit(main())
