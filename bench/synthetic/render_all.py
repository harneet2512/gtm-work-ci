"""Render a deal's kernel records into SourceEvents, exactly in the WP31 scheme (spec §e).

Emails: bare EmailMessage id, `opp:<id>` thread, crmarena-loader / wp31-v1, whole-second business-hour
times. CRM: `crm_change` with `opp:` / `contact:` / `account:` / `quote:` refs, keyed `field:<name>:<new>` or
`created`. Ids continue the real sequences (records.sequential_id). Only the envelope carries the marker.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from pathlib import Path
from typing import Any

from .base import BaseDeal
from .forward import DealResult, Record
from .paraphrase import Paraphraser
from .records import mark, sequential_id
from .render import CONNECTOR, CONNECTOR_VERSION, PROFILE, BaseEmail, Buyer, iso, render_reply, snap_time, tenant_address
from .seeding import Stream

MESSAGES = json.loads((Path(__file__).with_name("templates") / "messages.v1.json").read_text(encoding="utf-8"))
ROLE_C = {"champion": "Champion", "economic_buyer": "Economic buyer", "decision_maker": "Decision maker",
          "technical_evaluator": "Technical evaluator", "end_user": "User"}
TITLES = {"technical": (("Solutions Architect", "Engineering"), ("IT Manager", "IT"), ("Data Engineer", "Data Science")),
          "operations": (("Operations Manager", "Operations"), ("Supply Chain Lead", "Operations"),
                         ("Logistics Coordinator", "Operations")),
          "finance": (("Finance Manager", "Finance"), ("Procurement Lead", "Finance"), ("Financial Analyst", "Finance")),
          "business": (("Product Manager", "Project Management"), ("Marketing Director", "Marketing"),
                       ("Customer Success Manager", "Customer Success"))}
INTEGRATION = "integration:crmarena"  # = crmarena.IntegrationActor: the export has no per-user field history
FIRST = ("Alex", "Priya", "Daniel", "Mei", "Omar", "Sofia", "Lukas", "Amara", "Jonas", "Leila", "Mateo", "Hana")
LAST = ("Brooks", "Kapoor", "Novak", "Chen", "Haddad", "Rossi", "Weber", "Okoye", "Lindqvist", "Farah", "Silva", "Tanaka")
PREFIX = {"Contact": "003", "EmailMessage": "02s", "Quote": "0Q0"}


class Ids:
    """Allocates rendered ids after the real maximum of each prefix, in a deterministic order."""

    def __init__(self, quote_number_max: int = 0) -> None:
        self.rank: dict[str, int] = {}
        self.quote_number = quote_number_max

    def next_quote_number(self) -> str:
        self.quote_number += 1
        return f"{self.quote_number:08d}"

    def next(self, object_type: str, salt: str) -> str:
        facts = PROFILE["ids"][PREFIX[object_type]]
        k = self.rank.get(object_type, 0)
        self.rank[object_type] = k + 1
        return sequential_id(object_type, facts["max_body"], k, facts["median_gap"], salt)


@dataclass
class DealRender:
    bd: BaseDeal
    res: DealResult
    ids: Ids
    s: Stream
    reps: dict[str, str]  # lower-case rep email -> name (colleague pool)
    contacts: dict[str, dict[str, Any]]  # base contacts by id
    new: dict[str, dict[str, Any]] = field(default_factory=dict)  # kernel id of a synthetic contact -> rendered contact
    events: list[dict[str, Any]] = field(default_factory=list)
    last: datetime | None = None
    para: Paraphraser | None = None  # realism pass (paraphrase.py); None: template text

    @property
    def domain(self) -> str:
        emails = [c.get("Email", "") for c in self.contacts.values() if c.get("AccountId") == self.bd.deal.account_id]
        return next((e.split("@")[1] for e in emails if "@" in e), "example.com")

    def at(self, t: float) -> datetime:
        lo = self.bd.start + timedelta(days=t)
        if self.last is not None and lo <= self.last:
            lo = self.last
        out = snap_time(lo, lo + timedelta(days=1), self.s)
        self.last = out
        return out

    def person(self, cid: str, function: str) -> dict[str, Any]:
        if cid in self.contacts:
            c = self.contacts[cid]
            return {"id": cid, "email": c["Email"].lower(), "name": f'{c["FirstName"]} {c["LastName"]}', "title": c.get("Title")}
        if cid not in self.new:
            first, last = self.s.choice(FIRST), self.s.choice(LAST)
            title, dept = self.s.choice(TITLES.get(function, TITLES["business"]))
            self.new[cid] = {"id": self.ids.next("Contact", cid), "email": f"{first}.{last}@{self.domain}".lower(),
                             "name": f"{first} {last}", "title": title, "department": dept, "created": False}
        return self.new[cid]


def _env(system: str, object_id: str, key: str, at: datetime, payload: dict[str, Any]) -> dict[str, Any]:
    return mark({"source_system": system, "source_object_id": object_id, "source_event_key": key, "occurred_at": iso(at),
                 "connector": CONNECTOR, "connector_version": CONNECTOR_VERSION, "payload": payload})


def _crm(dr: DealRender, at: datetime, obj: str, record_id: str, key: str, fields: dict[str, Any], created: bool = False) -> None:
    """A crm_change exactly as WP31 builds it: integration actor, Contacts not tied to a deal."""
    p = {"kind": "crm_change", "object_type": obj, "record_id": record_id, "account_record_id": f"account:{dr.bd.deal.account_id}",
         "changed_at": iso(at), "changed_by": INTEGRATION, "created": created,
         "fields": {k: {"new": v} for k, v in fields.items() if v not in (None, "")}}
    if obj not in ("Opportunity", "Contact"):
        p["opportunity_record_id"] = f"opp:{dr.bd.deal.deal_id}"
    dr.events.append(_env("crm", record_id, key, at, p))


def _body(dr: DealRender, group: str, template: str, fill: dict[str, str]) -> str:
    """The template filled with its slots, or its accepted paraphrase (cassette) when the realism pass is on."""
    text = template.format(**fill)
    if dr.para is None:
        return text
    return dr.para.text(f"{group}#{hashlib.sha256(template.encode('utf-8')).hexdigest()[:8]}", fill, text)


def _email(dr: DealRender, at: datetime, inbound: bool, who: dict[str, Any], kind: str, cc: bool = False,
           other: str = "") -> None:
    t = MESSAGES[kind]
    acct = dr.bd.account.get("Name", "the company")
    product = dr.bd.opportunity.get("Name", "the project")
    seller_full = dr.bd.rep_name or dr.bd.rep_email.split("@")[0]
    fill = dict(seller_first=seller_full.split()[0], seller_full=seller_full, buyer_first=who["name"].split()[0],
                buyer_full=who["name"], company=acct, product=product, other_name=other)
    msg = dr.ids.next("EmailMessage", f"{dr.bd.deal.deal_id}|{kind}|{iso(at)}")
    rep = {"email": tenant_address(dr.bd.rep_email), **({"name": dr.bd.rep_name} if dr.bd.rep_name else {})}
    buyer = {"email": who["email"], "name": who["name"]}
    p = {"kind": "email", "message_id": msg, "thread_id": f"opp:{dr.bd.deal.deal_id}", "in_reply_to": None,
         "direction": "inbound" if inbound else "outbound", "from": buyer if inbound else rep,
         "to": [rep if inbound else buyer], "date": iso(at), "subject": t["subject"].format(**fill),
         "body_text": _body(dr, f"messages.{kind}", dr.s.choice(t["bodies"]), fill), "crm_opportunity_ref": f"opp:{dr.bd.deal.deal_id}"}
    if cc:
        pool = sorted(e for e in dr.reps if e != dr.bd.rep_email.lower())
        mate = dr.s.choice(pool) if pool else dr.bd.rep_email
        p["cc"] = [{"email": tenant_address(mate), **({"name": dr.reps[mate]} if dr.reps.get(mate) else {})}]
    dr.events.append(_env("email", msg, "received" if inbound else "sent", at, p))


def _contact_created(dr: DealRender, at: datetime, who: dict[str, Any]) -> None:
    if who.get("created", True):
        return
    who["created"] = True
    first, _, last = who["name"].partition(" ")
    _crm(dr, at, "Contact", f"contact:{who['id']}", "created",
         {"FirstName": first, "LastName": last, "Email": who["email"], "Title": who["title"], "Department": who["department"]},
         created=True)


def _reply(dr: DealRender, rec: Record, d: dict[str, Any]) -> None:
    at = dr.at(rec.t)
    who = dr.person(d["contact"], _fn(dr, d["contact"]))
    _contact_created(dr, at, who)
    prior = [e for e in dr.bd.emails if e.outbound and e.at <= at]
    base = prior[-1] if prior else (dr.bd.emails[0] if dr.bd.emails else None)
    if base is None:
        return
    until = dr.bd.start + timedelta(days=d["timing_until"]) if d.get("timing_until") else None
    kind = "objection" if d["reply_kind"] == "price_objection" else d["reply_kind"]
    email = BaseEmail(base.id, dr.bd.deal.deal_id, base.subject, dr.bd.rep_email, dr.bd.rep_name, base.at)
    buyer = Buyer(who["email"], who["name"], dr.bd.account.get("Name", "the company"), dr.bd.opportunity.get("Name", "the project"))
    msg = dr.ids.next("EmailMessage", f"{dr.bd.deal.deal_id}|reply|{iso(at)}")
    dr.events.append(render_reply(msg, at, kind, until, email, buyer, dr.s, dr.para))


def _quote(dr: DealRender, at: datetime) -> None:
    """A separate Quote record with the WP31 Quote fields (sequential QuoteNumber after the export's maximum)."""
    q = dr.ids.next("Quote", f"{dr.bd.deal.deal_id}|{iso(at)}")
    discount = round(5 + 10 * dr.s.uniform(), 8)
    total = round(float(dr.bd.opportunity.get("Amount") or 0) * (1 - discount / 100), 4)
    _crm(dr, at, "Quote", f"quote:{q}", "created",
         {"Name": f"{dr.bd.opportunity.get('Name', 'Proposal')} Quote", "QuoteNumber": dr.ids.next_quote_number(),
          "GrandTotal": total, "Discount": discount, "ExpirationDate": (at + timedelta(days=30)).strftime("%Y-%m-%d")},
         created=True)


def _title(dr: DealRender, at: datetime, who: dict[str, Any], title: str) -> None:
    """A Contact Title change (E2 move, E6 acting owner): CRM field:Title:<new>."""
    who["title"] = title
    _crm(dr, at, "Contact", f"contact:{who['id']}", f"field:Title:{title}", {"Title": title})


def _acting_title(dr: DealRender, at: datetime, who: dict[str, Any]) -> None:
    """E6: an acting owner. A base contact's Title changes; a new contact is created with the acting title."""
    current = who.get("title") or "Manager"
    title = current if current.startswith("Acting ") else f"Acting {current}"
    if title == current:
        return
    if who["id"] in dr.contacts or who.get("created"):  # an existing record changes; a new one is created acting
        _title(dr, at, who, title)
    else:
        who["title"] = title


def _fn(dr: DealRender, cid: str) -> str:
    return next((c.function for c in dr.bd.deal.contacts if c.id == cid), "business")


def render_deal(dr: DealRender, new_functions: dict[str, str]) -> list[dict[str, Any]]:
    """Every record of one deal, in kernel order. Deals with a real outcome get no stage path or outcome (Q2)."""
    synthetic_outcome = dr.res.outcome_source == "synthetic"
    for rec in dr.res.records:
        d = dict(rec.data)
        if rec.kind == "reply":
            _reply(dr, rec, d)
            continue
        at = dr.at(rec.t)
        if rec.kind in ("stage", "close"):
            if synthetic_outcome:  # the terminal stage carries Amount / CloseDate like the WP31 snapshot it supersedes
                extra = {"Amount": dr.bd.opportunity.get("Amount"), "CloseDate": dr.bd.opportunity.get("CloseDate")} \
                    if rec.kind == "close" else {}
                _crm(dr, at, "Opportunity", f"opp:{dr.bd.deal.deal_id}", f"field:StageName:{d['stage']}",
                     {"StageName": d["stage"], **extra})
        elif rec.kind in ("champion_named", "executive_joins", "stakeholder_enters", "expansion_interest"):
            who = dr.person(d["contact"], new_functions.get(d["contact"], "business"))
            if rec.kind == "champion_named" and d.get("acting"):
                _acting_title(dr, at, who)
            _contact_created(dr, at, who)
            if rec.kind in ("stakeholder_enters", "expansion_interest"):
                _email(dr, at, True, who, "stakeholder_intro" if rec.kind == "stakeholder_enters" else "expansion_interest")
        elif rec.kind == "champion_leaves":
            who = dr.person(d["contact"], _fn(dr, d["contact"]))
            _email(dr, at, True, who, "champion_leaves" if d.get("move") == "leaves" else "champion_moves")
            if d.get("move") == "moves":  # E2: the CRM records the new role in another area
                other = [f for f in sorted(TITLES) if f != _fn(dr, d["contact"])]
                _title(dr, at, who, dr.s.choice(TITLES[dr.s.choice(other)])[0])
        elif rec.kind == "reorg_announced":
            eb = next((c.id for c in dr.bd.deal.contacts if c.role == "economic_buyer"), None)
            if eb:
                _email(dr, at, True, dr.person(eb, _fn(dr, eb)), "reorg_announced")
        elif rec.kind == "written_handover":
            to = dr.person(d["to"], new_functions.get(d["to"], "technical"))
            _email(dr, at, True, dr.person(d["contact"], _fn(dr, d["contact"])), "written_handover", other=to["name"])
        elif rec.kind in ("seller_outreach", "seller_email"):
            target = d["to"]
            who = dr.person(target, new_functions.get(target, _fn(dr, target)))
            kind = {"ops_note": "seller_note", "value_case": "seller_value", "technical_reply": "seller_technical"}.get(d.get("purpose"), "seller_outreach")
            _email(dr, at, False, who, kind, cc=bool(d.get("cc_colleague")))
        elif rec.kind == "amount_edited":
            amount = float(dr.bd.opportunity.get("Amount") or 0)
            new = round(amount * (1 - (0.05 + 0.15 * dr.s.uniform())), 2)
            _crm(dr, at, "Opportunity", f"opp:{dr.bd.deal.deal_id}", f"field:Amount:{json.dumps(new)}", {"Amount": new})
        elif rec.kind == "quote_sent" and d.get("separate"):
            _quote(dr, at)
    return dr.events
