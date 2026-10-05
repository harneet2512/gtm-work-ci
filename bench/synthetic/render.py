"""Render kernel records into SourceEvents through EXACTLY the WP31 base scheme (spec §e, v1.1).

WP31 (core-go/internal/crmarena, PR #21) writes a base email as:
  source_object_id = message_id = the bare Salesforce EmailMessage id (02sWt...),
  thread_id = crm_opportunity_ref = 'opp:<opportunity id>', in_reply_to = the bare id it answers,
  key 'received'/'sent', connector 'crmarena-loader' / 'wp31-v1', whole-second UTC times on the
  business-hour clock of the source, rep addresses rebound to '<local>@vendor.example', no cc
  key when there is none, a name only when the CRM knows it.
A synthetic reply is rendered the same way: its id continues the real EmailMessage sequence
(records.sequential_id) and its time is snapped onto the real hour/minute mix inside the window
the kernel allows, so order relative to base events is kept. Only the envelope says 'synthetic'.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Mapping

from .records import mark
from .seeding import Stream

HERE = Path(__file__).resolve().parent
TEMPLATES = json.loads((HERE / "templates" / "replies.v1.json").read_text(encoding="utf-8"))
PROFILE = json.loads((HERE / "base_profile.v1.json").read_text(encoding="utf-8"))
CONNECTOR, CONNECTOR_VERSION = "crmarena-loader", "wp31-v1"  # = core-go/internal/crmarena/build.go
SELLER_DOMAIN = "vendor.example"  # = normalize.OurDomain


def tenant_address(rep_email: str) -> str:
    """A rep's address bound to our domain, keeping the local part (= crmarena.TenantAddress)."""
    local = rep_email.strip().lower().partition("@")[0]
    if not local:
        raise ValueError(f"{rep_email!r} has no local part")
    return f"{local}@{SELLER_DOMAIN}"


def _weighted(mix: Mapping[str, float], u: float) -> str:
    acc = 0.0
    for key, w in mix.items():
        acc += w
        if u < acc:
            return key
    return next(reversed(mix))


def snap_time(lo: datetime, hi: datetime, s: Stream, tries: int = 64) -> datetime:
    """A whole-minute business-hour time in (lo, hi] drawn from the real hour/minute mix; the first day
    with room is used. Falls back to lo + 1 minute when the window is too narrow for the grid."""
    day = lo.replace(hour=0, minute=0, second=0, microsecond=0)
    while day <= hi:
        for _ in range(tries):
            hh, mm = _weighted(PROFILE["email_hour_mix"], s.uniform()), _weighted(PROFILE["email_minute_mix"], s.uniform())
            t = day + timedelta(hours=int(hh), minutes=int(mm))
            if lo < t <= hi:
                return t
        day += timedelta(days=1)
    return (lo + timedelta(minutes=1)).replace(second=0, microsecond=0)


def iso(t: datetime) -> str:
    """Go's time.Time JSON form for whole-second UTC times, as WP31 writes them."""
    return t.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


@dataclass(frozen=True)
class BaseEmail:
    """The base outbound email a synthetic reply answers (fields of the WP31 export)."""
    id: str
    related_to: str  # opportunity id
    subject: str
    rep_address: str  # FromAddress as exported (techagents.com / techdomain.com)
    rep_name: str | None
    at: datetime


def _address(email: str, name: str | None) -> dict[str, str]:
    return {"email": email, "name": name} if name else {"email": email}


@dataclass(frozen=True)
class Buyer:
    email: str
    name: str | None
    company: str
    product: str  # a product named on the opportunity (OpportunityLineItem), as the buyer would call it


def _first(name: str | None, email: str) -> str:
    return (name or email.split("@")[0]).split()[0].split(".")[0].capitalize()


def render_reply(email_id: str, at: datetime, kind: str, until: datetime | None, base: BaseEmail,
                 buyer: Buyer, s: Stream, para: Any = None) -> dict[str, Any]:
    buyer_email, buyer_name = buyer.email, buyer.name
    template = s.choice(TEMPLATES[kind])
    slots = dict(seller_first=_first(base.rep_name, base.rep_address), buyer_full=buyer_name or _first(None, buyer_email),
                 company=buyer.company, product=buyer.product,
                 date=(until or at).strftime("%B %d").replace(" 0", " "), other_team="another team")
    body = template.format(**slots)
    if para is not None:  # realism pass: the accepted paraphrase of this (template, slots), else the template text
        body = para.text(f"replies.{kind}#{hashlib.sha256(template.encode('utf-8')).hexdigest()[:8]}", slots, body)
    subject = base.subject if base.subject.lower().startswith("re:") else f"Re: {base.subject}"
    payload = {
        "kind": "email", "message_id": email_id, "thread_id": f"opp:{base.related_to}", "in_reply_to": base.id,
        "direction": "inbound", "from": _address(buyer_email.lower(), buyer_name),
        "to": [_address(tenant_address(base.rep_address), base.rep_name)], "date": iso(at), "subject": subject,
        "body_text": body, "crm_opportunity_ref": f"opp:{base.related_to}",
    }
    return mark({"source_system": "email", "source_object_id": email_id, "source_event_key": "received",
                 "occurred_at": iso(at), "connector": CONNECTOR, "connector_version": CONNECTOR_VERSION,
                 "payload": payload})
