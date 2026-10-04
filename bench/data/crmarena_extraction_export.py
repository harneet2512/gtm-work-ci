"""Export the committed data bundle of a CRMArena extraction run (HAR-104 / HAR-130).

    pip install "psycopg[binary]"
    DATABASE_URL=postgres://...@127.0.0.1:.../ghost python bench/data/crmarena_extraction_export.py \\
        --export data/crmarena_b2b --worker-log worker.log --core-logs 'core*.log' --out bench/reports/crmarena-extraction-<date>-data

Reads (never writes) a local Postgres that holds the imported snapshot and the finished run, the worker's log and the exported
CRM records. Writes a bundle with NO email bodies: facts.json, events.jsonl.gz (one row per extractable email), claims.jsonl.gz
(one row per model claim, with the CRM values it is compared with and the agreement flags), states.jsonl.gz (one row per account)
and deal_values.json (every monetary value the CRM holds per deal). crmarena_extraction_recompute.py rebuilds every table of the
report from the bundle alone.
"""
from __future__ import annotations

import argparse
import glob
import gzip
import json
import os
import re
import sys
from collections import Counter
from datetime import timezone
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

sys.path.insert(0, str(Path(__file__).resolve().parent))
import crmarena_extraction_metrics as m  # noqa: E402

HELD_OUT = "held-out"
EXTRACTOR_VERSION = "extract-v4"
STATE_FIELDS = ["stage", "health", "owner", "motion", "champion", "champion_status", "economic_buyer", "blockers",
                "objections", "decision_criteria", "decision_process", "current_commitments", "next_milestone",
                "next_meeting", "relationship_risk", "product_use_case", "commercial_issue",
                "last_customer_interaction", "last_meaningful_change", "summary"]
UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
LOCAL_HOSTS = frozenset({"127.0.0.1", "localhost", "::1"})
VALUE_CHARS = 60
SYNONYMS = Path(__file__).resolve().parent / "crmarena_stage_synonyms.json"


def text_of(value: Any) -> str:
    if isinstance(value, str):
        return value
    if isinstance(value, dict):
        return str(value.get("text") or value.get("title") or value.get("name") or json.dumps(value, sort_keys=True))
    return json.dumps(value)


def iso(ts: Any) -> str:
    return ts.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def load_json(path: Path) -> list[dict]:
    return json.loads(path.read_text(encoding="utf-8"))


def write_jsonl_gz(path: Path, rows: list[dict]) -> None:
    with gzip.GzipFile(path, "wb", mtime=0) as gz:  # mtime 0: byte-identical re-exports
        for row in rows:
            gz.write((json.dumps(row, sort_keys=True, ensure_ascii=False) + "\n").encode("utf-8"))


class Db:
    def __init__(self) -> None:
        url = os.environ.get("DATABASE_URL", "")
        if urlparse(url).hostname not in LOCAL_HOSTS:
            raise SystemExit("refusing: DATABASE_URL must point at localhost")
        self.conn = m.utf8_connection(url, autocommit=True)

    def all(self, sql: str, args: tuple = ()) -> list[tuple]:
        return self.conn.execute(sql, args).fetchall()

    def one(self, sql: str, args: tuple = ()) -> tuple:
        return self.conn.execute(sql, args).fetchone()


class Truth:
    """The CRM side: deals, their monetary values, contact titles, and the uuid -> CRM id mappings."""

    def __init__(self, db: Db, export: Path) -> None:
        self.opps = {o["Id"]: o for o in load_json(export / "Opportunity.json")}
        self.contacts = {c["Id"]: c for c in load_json(export / "Contact.json")}
        values: dict[str, list[float]] = {k: [float(o["Amount"])] if o.get("Amount") is not None else [] for k, o in self.opps.items()}
        quote_deal = {q["Id"]: q.get("OpportunityId") for q in load_json(export / "Quote.json")}
        for q in load_json(export / "Quote.json"):
            self._add(values, q.get("OpportunityId"), q.get("GrandTotal"), q.get("TotalPrice"))
        for li in load_json(export / "OpportunityLineItem.json"):
            self._add(values, li.get("OpportunityId"), li.get("ListPrice"), li.get("UnitPrice"), li.get("TotalPrice"))
        for li in load_json(export / "QuoteLineItem.json"):
            self._add(values, quote_deal.get(li.get("QuoteId")), li.get("ListPrice"), li.get("UnitPrice"), li.get("TotalPrice"))
        self.values = {k: sorted({round(v, 2) for v in vs}) for k, vs in values.items()}
        self.opp_of = {str(e): k.removeprefix("opp:") for k, e in db.all(
            "SELECT source_key, entity_id FROM entity_source_mappings WHERE entity_type='opportunity' AND source_system='crm'")}
        self.contact_of = {str(e): k.removeprefix("contact:") for k, e in db.all(
            "SELECT source_key, entity_id FROM entity_source_mappings WHERE entity_type='person' AND source_system='crm' "
            "AND source_key LIKE 'contact:%%'")}
        self.account_of = {str(e): k.removeprefix("account:") for k, e in db.all(
            "SELECT source_key, entity_id FROM entity_source_mappings WHERE entity_type='account' AND source_system='crm'")}

    @staticmethod
    def _add(values: dict[str, list[float]], deal: str | None, *vals: Any) -> None:
        if deal in values:
            values[deal].extend(float(v) for v in vals if v is not None)


def stage_timeline(db: Db) -> dict[str, list[tuple[str, str]]]:
    """DERIVED stage events per deal: the loader's crm_explicit stage claims (the snapshot has no real stage history)."""
    out: dict[str, list[tuple[str, str]]] = {}
    for opp, value, at in db.all("SELECT opportunity_id::text, value, occurred_at FROM claims "
                                 "WHERE field_path='stage' AND standing='crm_explicit' AND opportunity_id IS NOT NULL"):
        out.setdefault(opp, []).append((iso(at), text_of(value)))
    return out


def claim_rows(db: Db, t: Truth, synonyms: dict) -> list[dict]:
    timeline = stage_timeline(db)
    people = {str(i): (n, ti or "") for i, n, ti in db.all("SELECT id, display_name, title FROM people")}
    owners = {str(i): str(o) for i, o in db.all("SELECT id, owner_person_id FROM opportunities")}
    rows = []
    q = ("SELECT c.id::text, c.source_activity_id::text, c.account_id::text, c.opportunity_id::text, c.field_path, c.value, "
         "c.evidence_quote, c.occurred_at, c.subject_person_id::text, c.speaker_person_id::text, a.body_text "
         "FROM claims c JOIN activities a ON a.id = c.source_activity_id WHERE c.standing='first_party_ai' ORDER BY c.id")
    for cid, act, acct, opp, field, value, quote, at, subject, speaker, body in db.all(q):
        text = text_of(value)
        crm_opp = t.opps.get(t.opp_of.get(opp or "", ""))
        row: dict[str, Any] = {
            "claim_id": cid, "activity_id": act, "account_id": acct, "crm_account_id": t.account_of.get(acct),
            "deal_id": opp, "crm_deal_id": t.opp_of.get(opp or ""), "field": field, "value": text[:VALUE_CHARS],
            "occurred_at": iso(at), "quote_verbatim": quote in (body or ""),
            "subject_resolved": (subject is not None) if field in m.PERSON_FIELDS else None,
            "speaker_resolved": speaker is not None, "stated_amounts": m.parse_amounts(f"{text} {quote or ''}")[:6]}
        if crm_opp:
            in_force = m.stage_in_force(timeline.get(opp, []), iso(at))
            row.update(crm_stage_final=crm_opp["StageName"], crm_stage_in_force=in_force, crm_amount=crm_opp.get("Amount"))
        if field == "stage" and crm_opp:
            row["stage_claim"] = text
        if field == "owner" and opp:
            claimed = people.get(text, (text, ""))[0] if UUID.match(text) else text
            row.update(owner_claimed=claimed, crm_owner=people.get(owners.get(opp, ""), ("", ""))[0])
        if field in ("stakeholder_role", "buying_group.member") and subject:
            row["crm_title"] = t.contacts.get(t.contact_of.get(subject, ""), {}).get("Title") or people.get(subject, ("", ""))[1]
        row.update({f"ok_{k}": v for k, v in m.claim_flags(row, synonyms, t.values).items()})
        rows.append(row)
    return rows


def event_rows(db: Db) -> list[dict]:
    stored = Counter((a, f, q) for a, f, q in db.all(
        "SELECT source_activity_id::text, field_path, evidence_quote FROM claims WHERE standing = 'first_party_ai'"))
    cached = {a: (o, b) for a, o, b in db.all(
        "SELECT c.activity_id::text, c.output, a.body_text FROM extraction_cache c JOIN activities a ON a.id = c.activity_id "
        "WHERE c.model <> %s AND c.extractor_version = %s", (HELD_OUT, EXTRACTOR_VERSION))}
    rows = []
    for act, typ, acct in db.all("SELECT id::text, activity_type, account_id::text FROM activities WHERE activity_type IN "
                                 "('EmailReceived','EmailReply','EmailSent') AND btrim(coalesce(body_text,'')) <> '' ORDER BY id"):
        row: dict[str, Any] = {"activity_id": act, "type": typ, "account_id": acct, "extracted": act in cached}
        if act in cached:
            output, body = cached[act]
            cands = output.get("claims") or []
            kept = 0
            causes: Counter[str] = Counter()
            for c in cands:
                key = (act, c.get("field_path"), c.get("evidence_quote"))
                if stored[key] > 0:
                    stored[key] -= 1
                    kept += 1
                else:
                    causes["quote_not_verbatim_in_body" if (c.get("evidence_quote") or "") not in (body or "") else "core_validation_or_duplicate"] += 1
            row.update(candidates=len(cands), worker_dropped=int(output.get("dropped") or 0), rejected=int(output.get("rejected") or 0),
                       stored=kept, core_drop_causes=dict(causes))
        rows.append(row)
    return rows


def state_rows(db: Db, t: Truth) -> list[dict]:
    people = {str(i): n for i, n in db.all("SELECT id, display_name FROM people")}
    owners = {str(i): str(o) for i, o in db.all("SELECT id, owner_person_id FROM opportunities")}
    act_opp = dict(db.all("SELECT id::text, opportunity_id::text FROM activities WHERE opportunity_id IS NOT NULL"))
    rows = []
    for acct, opp, state in db.all("SELECT account_id::text, opportunity_id::text, state FROM account_state ORDER BY account_id"):
        fields = state.get("fields", {})
        row: dict[str, Any] = {"account_id": acct, "deal_id": opp, "fields": {
            n: [bool((fields.get(n) or {}).get("known")), (fields.get(n) or {}).get("standing")] for n in STATE_FIELDS}}
        crm_opp = t.opps.get(t.opp_of.get(opp or "", ""))
        st, ow = fields.get("stage", {}), fields.get("owner", {})
        if crm_opp and st.get("known"):
            refs = st.get("evidence_refs") or []
            row.update(state_stage=text_of(st["value"]), crm_stage_of_pointed_deal=crm_opp["StageName"],
                       stage_winner_same_deal=bool(refs and act_opp.get(refs[0].get("activity_id")) == opp))
        if crm_opp and ow.get("known"):
            row.update(state_owner=people.get(str(ow["value"]), str(ow["value"])), crm_owner_of_pointed_deal=people.get(owners.get(opp, ""), ""))
        rows.append(row)
    return rows


def facts(db: Db, export: Path, args: argparse.Namespace, deals: dict[str, list[float]]) -> dict:
    manifest = json.loads((export / "manifest.json").read_text(encoding="utf-8"))
    by_type = dict(db.all("SELECT activity_type, count(*) FROM activities GROUP BY 1 ORDER BY 2 DESC"))
    cache = db.all("SELECT model, extractor_version, count(*) FROM extraction_cache GROUP BY 1,2")
    deals_per = db.one("SELECT round(avg(n),2), max(n) FROM (SELECT count(*) n FROM opportunities GROUP BY account_id) d")
    return {
        "dataset": {"name": manifest.get("dataset"), "licence": manifest.get("licence"), "exported_at": manifest.get("exported_at")},
        "activities_by_type": by_type,
        "models": {f"{mo}@{v}": n for mo, v, n in cache if mo != HELD_OUT},
        "quarantined": [{"reason": r, "permanent": p, "count": n} for r, p, n in db.all(
            "SELECT reason, permanent, count(*) FROM quarantined_activities GROUP BY 1,2")],
        "parked_jobs": db.one("SELECT count(*) FROM recompute_jobs WHERE parked_at IS NOT NULL")[0],
        "pending_jobs": db.one("SELECT count(*) FROM recompute_jobs")[0],
        "unresolved_activities": db.one("SELECT count(*) FROM unresolved_activities")[0],
        "deals_per_account": {"mean": float(deals_per[0]), "max": deals_per[1]},
        "claims_by_extractor_standing": [[e, s, n] for e, s, n in db.all(
            "SELECT split_part(extractor, ':', 1), standing, count(*) FROM claims GROUP BY 1,2 ORDER BY 3 DESC, 1, 2")],
        "claims_by_status": dict(db.all("SELECT status, count(*) FROM claims WHERE standing='first_party_ai' GROUP BY 1")),
        "email_participants": {r: {"rows": n, "with_person_id": h} for r, n, h in db.all(
            "SELECT role, count(*), count(person_id) FROM activity_participants p JOIN activities a ON a.id = p.activity_id "
            "WHERE a.activity_type IN ('EmailReceived','EmailReply','EmailSent') GROUP BY 1")},
        "worker": worker_facts(args),
        "notes": args.note,
    }


def worker_facts(args: argparse.Namespace) -> dict:
    log = m.parse_worker_log(Path(args.worker_log).read_text(encoding="utf-8", errors="replace").splitlines())
    log["latency_s"] = [round(x, 3) for x in log["latency_s"]]
    core: Counter[str] = Counter()
    for pattern in args.core_logs or []:
        for path in sorted(glob.glob(pattern)):
            for line in Path(path).read_text(encoding="utf-8", errors="replace").splitlines():
                try:
                    rec = json.loads(line)
                except ValueError:
                    continue
                if rec.get("level") in ("WARN", "ERROR"):
                    core[f"{rec['level']}: {str(rec.get('msg'))[:80]}"] += 1
    return {"log": log, "core_warnings_and_errors": dict(core),
            "prices_usd_per_token": {"prompt": args.usd_per_prompt_token, "completion": args.usd_per_completion_token},
            "key_meter": {"before": args.spend_before, "after": args.spend_after}}


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--export", type=Path, required=True)
    ap.add_argument("--worker-log", required=True)
    ap.add_argument("--core-logs", action="append")
    ap.add_argument("--spend-before", type=float)
    ap.add_argument("--spend-after", type=float)
    ap.add_argument("--usd-per-prompt-token", type=float, default=0.028e-6)
    ap.add_argument("--usd-per-completion-token", type=float, default=0.056e-6)
    ap.add_argument("--note", action="append", default=[])
    ap.add_argument("--out", type=Path, required=True)
    args = ap.parse_args()
    db = Db()
    truth = Truth(db, args.export)
    synonyms = json.loads(SYNONYMS.read_text(encoding="utf-8"))
    args.out.mkdir(parents=True, exist_ok=True)
    write_jsonl_gz(args.out / "claims.jsonl.gz", claim_rows(db, truth, synonyms))
    write_jsonl_gz(args.out / "events.jsonl.gz", event_rows(db))
    write_jsonl_gz(args.out / "states.jsonl.gz", state_rows(db, truth))
    deal_values = {"values": truth.values, "amount": {k: o.get("Amount") for k, o in sorted(truth.opps.items())}}
    (args.out / "deal_values.json").write_text(json.dumps(deal_values, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")
    (args.out / "facts.json").write_text(json.dumps(facts(db, args.export, args, truth.values), indent=1, sort_keys=True, default=str) + "\n", encoding="utf-8")
    print("wrote bundle", args.out)


if __name__ == "__main__":
    main()
