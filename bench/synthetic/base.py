"""Load the WP31 CRMArena-Pro export (read-only) into kernel inputs (generation stage 1-2).

Each opportunity becomes a DealInput on its own clock (day 0 = the deal's start), with real
customer emails as base data, roles from real titles (roles.py), the account's real quotes, the
visible real outcome (Accepted quote or linked contract = won, Rejected/Denied quote = lost) and
the base-data null features. Synthetic-only draws (reorg dates, account offset, technical line)
come from named seeded streams, so the result is deterministic for a given export and seed.
"""
from __future__ import annotations

import hashlib
import json
import math
from collections import Counter, defaultdict
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Mapping

from . import roles as R
from .plan import Contact, DealInput, Inbound, Outbound
from .seeding import Stream, stream

REP_DOMAINS = ("techagents.com", "techdomain.com")
OBJECTS = ("Account", "Contact", "User", "Opportunity", "EmailMessage", "Quote")


def _load(export: Path, name: str) -> list[dict[str, Any]]:
    path = export / f"{name}.json"
    return json.loads(path.read_text(encoding="utf-8")) if path.exists() else []


def export_hash(export: Path) -> str:
    """sha256 over the sorted (name, sha256) of every export file: the manifest's base-data hash."""
    h = hashlib.sha256()
    for path in sorted(export.glob("*.json")):
        h.update(path.name.encode("utf-8"))
        h.update(hashlib.sha256(path.read_bytes()).digest())
    return h.hexdigest()


def when(value: str) -> datetime:
    """A Salesforce datetime (2023-11-06T11:15:00.000+0000) or date (2023-11-10) as UTC."""
    if len(value) == 10:
        return datetime.strptime(value, "%Y-%m-%d").replace(tzinfo=timezone.utc)
    return datetime.strptime(value, "%Y-%m-%dT%H:%M:%S.%f%z").astimezone(timezone.utc)


def is_rep(address: str | None) -> bool:
    return (address or "").strip().lower().rpartition("@")[2] in REP_DOMAINS


@dataclass(frozen=True)
class BaseEmailRec:
    id: str
    at: datetime
    sender: str
    recipient: str
    subject: str
    outbound: bool


@dataclass(frozen=True)
class BaseDeal:
    """One opportunity: the kernel input plus what rendering and scoring need from the base."""
    deal: DealInput
    start: datetime
    opportunity: Mapping[str, Any]
    account: Mapping[str, Any]
    emails: tuple[BaseEmailRec, ...]
    rep_email: str
    rep_name: str | None
    nulls: tuple[tuple[str, int], ...]
    roles: tuple[tuple[str, str], ...]  # (contact id, role) for this deal


@dataclass(frozen=True)
class Base:
    deals: tuple[BaseDeal, ...]
    contacts: Mapping[str, Mapping[str, Any]]
    users: Mapping[str, str]  # lower-case rep email -> name
    export_sha256: str
    quote_number_max: int = 0  # synthetic Quotes continue the export's QuoteNumber sequence


def _real_outcome(opp: Mapping[str, Any], quotes: list[dict]) -> str | None:
    statuses = {q.get("Status") for q in quotes}
    if opp.get("ContractId__c") or "Accepted" in statuses:
        return "won"
    return "lost" if statuses & {"Rejected", "Denied"} else None


def _reorgs(proc: Mapping[str, float], s: Stream, lo: datetime, hi: datetime) -> list[datetime]:
    rate, t, out = proc["reorg_rate_per_year"] / 365.0, 0.0, []
    span = (hi - lo).days + 200
    while True:
        t += -math.log(1.0 - s.uniform()) / rate
        if t > span:
            return out
        out.append(lo + timedelta(days=t))


def _emails_by_deal(emails: list[dict]) -> dict[str, list[BaseEmailRec]]:
    out: dict[str, list[BaseEmailRec]] = defaultdict(list)
    for e in sorted(emails, key=lambda e: (e["MessageDate"], e["Id"])):
        if not e.get("RelatedToId"):
            continue
        out[e["RelatedToId"]].append(BaseEmailRec(e["Id"], when(e["MessageDate"]), (e.get("FromAddress") or "").lower(),
                                                  (e.get("ToAddress") or "").split(";")[0].strip().lower(),
                                                  e.get("Subject") or "", is_rep(e.get("FromAddress"))))
    return out


def load(export: Path, proc: Mapping[str, float], seed: int, sd: float) -> Base:
    data = {name: _load(export, name) for name in OBJECTS}
    accounts = {a["Id"]: a for a in data["Account"]}
    contacts = {c["Id"]: c for c in data["Contact"]}
    by_email = {(c.get("Email") or "").lower(): c["Id"] for c in data["Contact"]}
    users = {(u.get("Email") or "").lower(): (u.get("Name") or "").strip() for u in data["User"] if is_rep(u.get("Email"))}
    user_by_id = {u["Id"]: (u.get("Email") or "").lower() for u in data["User"]}
    quotes: dict[str, list[dict]] = defaultdict(list)
    for q in data["Quote"]:
        quotes[q.get("OpportunityId") or ""].append(q)
    emails = _emails_by_deal(data["EmailMessage"])
    opps = sorted(data["Opportunity"], key=lambda o: (o["AccountId"], o["CreatedDate"], o["Id"]))
    amounts = sorted(float(o.get("Amount") or 0) for o in opps)
    band = lambda a: sum(a > amounts[int(q * (len(amounts) - 1))] for q in (0.25, 0.5, 0.75)) if amounts else 0  # noqa: E731
    out: list[BaseDeal] = []
    by_account: dict[str, list[dict]] = defaultdict(list)
    for o in opps:
        by_account[o["AccountId"]].append(o)
    for acct, deals in sorted(by_account.items()):
        out += _account_deals(acct, deals, accounts, contacts, by_email, users, user_by_id, quotes, emails, band, proc, seed, sd)
    qmax = max((int(q["QuoteNumber"]) for q in data["Quote"] if str(q.get("QuoteNumber") or "").isdigit()), default=0)
    return Base(tuple(out), contacts, users, export_hash(export), qmax)


def _account_deals(acct, deals, accounts, contacts, by_email, users, user_by_id, quotes, emails, band, proc, seed, sd):
    s = stream(seed, "base", "account", acct)
    people = [R.Person(cid, *R.parse_title(c.get("Title"))) for cid, c in sorted(contacts.items()) if c.get("AccountId") == acct]
    acct_roles = R.account_roles(people, s.uniform() < 0.5, s) if people else {}
    effect = s.normal(0.0, sd)
    starts = [min([when(o["CreatedDate"]), *(e.at for e in emails.get(o["Id"], [])[:1])]) for o in deals]
    reorgs = _reorgs(proc, s, min(starts), max(starts)) if starts else []
    real_quotes = [(q.get("OpportunityId"), when(q["CreatedDate"])) for o in deals for q in quotes.get(o["Id"], [])]
    out = []
    for o, start in zip(deals, starts):
        recs = tuple(emails.get(o["Id"], []))
        close = max([when(o["CloseDate"]) + timedelta(hours=23), *(e.at + timedelta(days=1) for e in recs)])
        days = max(1, math.ceil((close - start).total_seconds() / 86400))
        sent = Counter(by_email.get(e.recipient) for e in recs if e.outbound)
        champ = R.deal_champion(people, acct_roles, o.get("ContactId"), {k: v for k, v in sent.items() if k})
        roles = {pid: ("champion" if pid == champ else role) for pid, role in acct_roles.items()}
        fn = {p.id: p.function for p in people}
        rel = lambda at: round((at - start).total_seconds() / 86400, 4)  # noqa: E731
        outbound = tuple(Outbound(rel(e.at), (by_email.get(e.recipient, e.recipient),)) for e in recs if e.outbound)
        inbound = tuple(Inbound(rel(e.at), by_email.get(e.sender, e.sender)) for e in recs if not e.outbound)
        sibling = any(start - timedelta(days=30) <= x < start for x in starts if x != start)
        deal = DealInput(o["Id"], acct, days, outbound, inbound, tuple(Contact(pid, roles[pid], fn[pid]) for pid in sorted(roles)),
                         tuple(rel(r) for r in reorgs), sibling, effect,
                         account_quote_days=tuple(sorted(rel(at) for opp, at in real_quotes if opp != o["Id"])),
                         real_outcome=_real_outcome(o, quotes.get(o["Id"], [])))
        rep = user_by_id.get(o.get("OwnerId") or "") or next((e.sender for e in recs if e.outbound), "rep@techagents.com")
        first_out = next((e.at for e in recs if e.outbound), start)
        nulls = (("industry", hash_bucket(accounts.get(acct, {}).get("Industry"))), ("amount_band", band(float(o.get("Amount") or 0))),
                 ("owner", hash_bucket(o.get("OwnerId"))), ("weekday", first_out.weekday()))
        out.append(BaseDeal(deal, start, o, accounts.get(acct, {}), recs, rep, users.get(rep), nulls, tuple(sorted(roles.items()))))
    return out


def hash_bucket(value: Any, n: int = 64) -> int:
    """A stable small integer for a categorical base value (null features)."""
    return int.from_bytes(hashlib.sha256(str(value).encode("utf-8")).digest()[:2], "big") % n
