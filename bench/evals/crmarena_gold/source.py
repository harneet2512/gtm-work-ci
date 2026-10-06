"""The CRMArena snapshot as the WP31 loader dates it (HAR-114 gold v2).

Reads the git-ignored export `data/crmarena_b2b/` (bench/data/crmarena_export.py) and exposes, per deal, its events in
the loader's order and dating (docs/data/crmarena-b2b.md "What the loader derives"):

- an email is dated at MessageDate; it is inbound when the sender is not a rep (a rep is a @techagents.com or
  @techdomain.com address; the loader rebinds them to @vendor.example);
- a task is dated at its ActivityDate, midnight UTC (it is a due date: no task is ever completed);
- a quote is dated at its CreatedDate;
- stage, quote Status, Amount and CloseDate are dated at the deal's LAST event by the loader, so a decision context
  never contains them. This module deliberately does not expose them.

All ids in a case are deterministic uuid5 values of 'crmarena:<kind>:<Salesforce Id>'.
"""
from __future__ import annotations

import hashlib
import json
import uuid
from collections import defaultdict
from dataclasses import dataclass
from functools import cached_property
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[3]
SNAPSHOT = ROOT / "data" / "crmarena_b2b"
SPLIT = ROOT / "bench" / "data" / "deal_split.json"
NAMESPACE = uuid.uuid5(uuid.NAMESPACE_URL, "https://github.com/harneet2512/gtm-work/crmarena")
REP_DOMAINS = ("@techagents.com", "@techdomain.com")
VENDOR_DOMAIN = "vendor.example"
KIND_ORDER = {"quote": 0, "contract": 1, "email": 2, "task": 3}


def uid(kind: str, source_id: str) -> str:
    return str(uuid.uuid5(NAMESPACE, f"crmarena:{kind}:{source_id}"))


def iso(stamp: str) -> str:
    """Salesforce '2024-03-22T10:00:00.000+0000' (or a bare date) -> '2024-03-22T10:00:00Z'."""
    day, _, rest = stamp.partition("T")
    return f"{day}T{rest[:8] if rest else '00:00:00'}Z"


@dataclass(frozen=True)
class Event:
    kind: str  # email | task | quote | contract
    sf_id: str
    deal_id: str
    occurred_at: str  # ISO, loader dating
    subject: str
    body: str
    inbound: bool = False
    sender: str = ""
    recipient: str = ""

    @property
    def sobject(self) -> str:
        return {"email": "EmailMessage", "task": "Task", "quote": "Quote", "contract": "Contract"}[self.kind]

    @property
    def activity_type(self) -> str:
        if self.kind == "task":
            return "CRMTaskLogged"
        if self.kind == "quote":
            return "QuoteCreated"
        if self.kind == "contract":
            return "ContractSigned"
        reply = self.subject.lower().startswith("re:")
        if self.inbound:
            return "EmailReply" if reply else "EmailReceived"
        return "EmailReply" if reply else "EmailSent"


def is_rep(address: str) -> bool:
    return address.lower().endswith(REP_DOMAINS)


class Snapshot:
    """Lazy, read-only view of the export plus the frozen deal split."""

    def __init__(self, root: Path = SNAPSHOT) -> None:
        self.root = root

    def load(self, name: str) -> list[dict[str, Any]]:
        return json.loads((self.root / f"{name}.json").read_text(encoding="utf-8"))

    @cached_property
    def manifest_sha256(self) -> str:
        return hashlib.sha256((self.root / "manifest.json").read_bytes()).hexdigest()

    @cached_property
    def file_hashes(self) -> dict[str, str]:
        return dict(json.loads((self.root / "manifest.json").read_text(encoding="utf-8"))["sha256"])

    @cached_property
    def split(self) -> dict[str, Any]:
        return json.loads(SPLIT.read_text(encoding="utf-8"))

    @cached_property
    def accounts(self) -> dict[str, dict[str, Any]]:
        return {r["Id"]: r for r in self.load("Account")}

    @cached_property
    def opportunities(self) -> dict[str, dict[str, Any]]:
        return {r["Id"]: r for r in self.load("Opportunity")}

    @cached_property
    def contacts(self) -> dict[str, dict[str, Any]]:
        return {r["Id"]: r for r in self.load("Contact")}

    @cached_property
    def contacts_by_email(self) -> dict[str, dict[str, Any]]:
        return {r["Email"].lower(): r for r in self.contacts.values()}

    @cached_property
    def users_by_email(self) -> dict[str, dict[str, Any]]:
        out: dict[str, dict[str, Any]] = {}
        for r in sorted(self.load("User"), key=lambda r: r["Id"]):
            out.setdefault((r.get("Email") or "").lower(), r)
        return out

    @cached_property
    def users(self) -> dict[str, dict[str, Any]]:
        return {r["Id"]: r for r in self.load("User")}

    @cached_property
    def events_by_deal(self) -> dict[str, list[Event]]:
        deals: dict[str, list[Event]] = defaultdict(list)
        for e in self.load("EmailMessage"):
            inbound = not is_rep(e["FromAddress"])
            deals[e["RelatedToId"]].append(Event(
                "email", e["Id"], e["RelatedToId"], iso(e["MessageDate"]), e["Subject"] or "", e["TextBody"] or "",
                inbound, e["FromAddress"].lower(), e["ToAddress"].lower()))
        for t in self.load("Task"):
            deals[t["WhatId"]].append(Event("task", t["Id"], t["WhatId"], iso(t["ActivityDate"]), t["Subject"] or "",
                                            t["Description"] or ""))
        for q in self.load("Quote"):
            if q.get("OpportunityId"):
                deals[q["OpportunityId"]].append(Event("quote", q["Id"], q["OpportunityId"], iso(q["CreatedDate"]),
                                                       q["Name"] or "", q.get("Description") or ""))
        contracts = {c["Id"]: c for c in self.load("Contract")}
        for o in self.opportunities.values():
            c = contracts.get(o.get("ContractId__c") or "")
            if c:  # the loader dates a contract when both parties have signed
                signed = [d for d in (c.get("CustomerSignedDate"), c.get("CompanySignedDate")) if d]
                deals[o["Id"]].append(Event("contract", c["Id"], o["Id"], iso(max(signed) if signed else c["StartDate"]),
                                            f"Contract {c['ContractNumber']}", c.get("Description") or ""))
        return {k: sorted(v, key=lambda e: (e.occurred_at, KIND_ORDER[e.kind], e.sf_id)) for k, v in deals.items()}

    def deal_events(self, deal_id: str) -> list[Event]:
        return self.events_by_deal[deal_id]

    def event(self, deal_id: str, sf_id: str) -> Event:
        return next(e for e in self.deal_events(deal_id) if e.sf_id == sf_id)

    def account_of(self, deal_id: str) -> dict[str, Any]:
        return self.accounts[self.opportunities[deal_id]["AccountId"]]

    def account_contacts(self, account_id: str) -> list[dict[str, Any]]:
        return sorted((c for c in self.contacts.values() if c["AccountId"] == account_id), key=lambda c: c["Id"])

    def rep_of(self, deal_id: str) -> dict[str, Any]:
        """The deal's rep: the owner of the opportunity (a User record)."""
        return self.users[self.opportunities[deal_id]["OwnerId"]]

    def previous_deals(self) -> list[str]:
        return list(self.split["previous_deal_ids"])

    def current_deals(self) -> list[str]:
        return list(self.split["current_deal_ids"])


def excerpt(body: str, limit: int) -> str:
    """Leading, verbatim excerpt of `body` of at most `limit` characters, cut at a paragraph or sentence end."""
    if len(body) <= limit:
        return body
    head = body[:limit]
    for sep in ("\n\n", ". ", "\n"):
        cut = head.rfind(sep)
        if cut > limit // 3:
            return head[: cut + (1 if sep == ". " else 0)].rstrip()
    return head.rstrip()


def vendor_address(user: dict[str, Any]) -> str:
    return f"{user['Email'].split('@')[0].lower()}@{VENDOR_DOMAIN}"
