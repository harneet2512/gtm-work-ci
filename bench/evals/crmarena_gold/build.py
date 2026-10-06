"""Spec -> unlabelled case (HAR-114 gold v2, part B).

A spec (see specs_*.py) names a real deal, a trigger record and a decision time; this module assembles the situation
from the snapshot with the loader's dating, keeping ONLY events at or before `now`, and turns the authored candidate
into an agent_run_output. Expected judgments are added later from the labelling passes (labels.py).

Spec keys:
  id, type, title, deal, trigger (sObject Id of an email or task of the deal), now (ISO or None: the trigger's time,
  09:00Z for a task), support [(Id, char limit | None)], state {fields, gaps, group_extra [(contact Id, roles)],
  champion_status, champion_basis}, recent (summary, material_diff_fields, signals), commitments [(text, owner, due,
  status)], stated_timing, knowledge [keys], cand {...}, best {...}, evals [eval types to label], intent (design
  intent: should the candidate pass), difficulty, extra [tags], notes, gap (evidence gap).
"""
from __future__ import annotations

from typing import Any

from .knowledge import KNOWLEDGE
from .source import Event, Snapshot, excerpt, uid, vendor_address

CUTOFF = "2023-11-01"
GENERATOR = "bench/evals/crmarena_gold v1"
ATTRIBUTION = ("Salesforce AI Research, CRMArena-Pro (https://github.com/SalesforceAIResearch/CRMArena, LICENSE.txt); "
               "records selected and excerpted, nothing edited")
TRIGGER_LIMIT = 2600
SUPPORT_LIMIT = 1700
STATE_LISTS = ("blockers", "objections", "decision_criteria", "current_commitments")
KIND_PREFIX = {"email": "email", "task": "task", "quote": "quote"}


class SpecError(ValueError):
    """The spec contradicts the snapshot (a quote that is not verbatim, an event after `now`, ...)."""


def person_for(snap: Snapshot, deal: str, event: Event) -> str | None:
    if event.kind != "email":
        return None
    address = event.sender
    contact = snap.contacts_by_email.get(address)
    if contact:
        return uid("person", contact["Id"])
    return uid("person", f"rep:{address.split('@')[0]}")


def rep_person(snap: Snapshot, deal: str) -> str:
    return uid("person", f"rep:{snap.rep_of(deal)['Email'].split('@')[0].lower()}")


def decision_time(event: Event, override: str | None) -> str:
    if override:
        return override
    return f"{event.occurred_at[:10]}T09:00:00Z" if event.kind == "task" else event.occurred_at


def activity(snap: Snapshot, deal: str, event: Event, limit: int) -> dict[str, Any]:
    text = excerpt(event.body, limit)
    if text not in event.body:
        raise SpecError(f"excerpt of {event.sf_id} is not verbatim")
    out = {"activity_id": uid(event.kind, event.sf_id), "activity_type": event.activity_type,
           "occurred_at": event.occurred_at, "actor_person_id": person_for(snap, deal, event),
           "event_file": f"crmarena:{event.sobject}:{event.sf_id}", "synthetic": False, "text": text}
    if event.subject:
        out["subject"] = event.subject
    out["date_semantics"] = {"email": "sent", "task": "task due date", "quote": "quote created", "contract": "contract signed"}[event.kind]
    return out


def visible(snap: Snapshot, deal: str, now: str) -> list[Event]:
    return [e for e in snap.deal_events(deal) if e.occurred_at <= now]


def correspondents(snap: Snapshot, events: list[Event]) -> list[dict[str, Any]]:
    """Customer contacts on the deal's emails up to now, in order of first appearance."""
    seen: dict[str, dict[str, Any]] = {}
    for e in events:
        if e.kind == "email":
            contact = snap.contacts_by_email.get(e.sender if e.inbound else e.recipient)
            if contact:
                seen.setdefault(contact["Id"], contact)
    return list(seen.values())


def member(contact: dict[str, Any], roles: list[str], status: str, last: str | None, refs: list[dict], note: str = "") -> dict[str, Any]:
    """A buying-group member. `note` marks a role CRMArena does not record: it goes into the title, a free-text field of the
    existing schema that judges see (the schema has no confidence field and the judge code is not changed)."""
    title = contact.get("Title")
    return {"person_id": uid("person", contact["Id"]), "display_name": f"{contact['FirstName']} {contact['LastName']}",
            "title": f"{title} ({note})" if note and title else title, "roles": roles, "status": status, "delegated_to_person_id": None,
            "last_engaged_at": last, "evidence_refs": refs}


def build_state(snap: Snapshot, spec: dict[str, Any], events: list[Event], now: str) -> tuple[dict, dict]:
    deal, st = spec["deal"], spec.get("state", {})
    opp, acct = snap.opportunities[deal], snap.account_of(deal)
    people = correspondents(snap, events)
    inbound = [e for e in events if e.kind == "email" and e.inbound]
    champion = people[0] if len(people) == 1 and inbound else None
    last_in = inbound[-1] if inbound else None
    refs = [{"activity_id": uid("email", last_in.sf_id), "occurred_at": last_in.occurred_at}] if last_in else []
    group = [member(c, ["champion"] if champion and c["Id"] == champion["Id"] else ["unknown"],
                    "active" if last_in else "unknown", last_in.occurred_at if last_in else None, refs,
                    "champion role inferred, not recorded" if champion and c["Id"] == champion["Id"] else "") for c in people]
    for contact_id, roles in st.get("group_extra", []):
        group.append(member(snap.contacts[contact_id], roles, "unknown", None, [], "" if roles == ["unknown"] else "role inferred from department"))
    champion_status = st.get("champion_status", "active" if champion else "unknown")
    fields: dict[str, Any] = {
        "stage": "unknown", "health": "unknown", "motion": "unknown",
        "champion": uid("person", champion["Id"]) if champion else "unknown",
        "champion_status": champion_status, "economic_buyer": "unknown", "relationship_risk": "unknown"}
    for name, value in st.get("fields", {}).items():
        fields[name] = [dict(item) for item in value] if isinstance(value, list) else value
    state = {"account_id": uid("account", acct["Id"]), "account_name": acct["Name"],
             "opportunity_id": uid("opportunity", opp["Id"]), "fields": fields, "buying_group": group,
             "coverage_gaps": list(st.get("gaps", ["economic_buyer"])),
             **({"conflicts": list(st["conflicts"])} if st.get("conflicts") else {})}
    basis = st.get("champion_basis", f"inferred, not recorded: the only customer correspondent "
                   f"({len(inbound)} inbound emails); CRMArena has no roles")
    provenance = {"champion": basis if champion else "no customer correspondent yet",
                  "stage": "unknown unless a field below says otherwise: the loader dates the stage at the deal's last event",
                  **{k: v for k, v in st.get("provenance", {}).items()}}
    return state, provenance


def timeline(snap: Snapshot, events: list[Event], spec: dict[str, Any]) -> dict[str, Any]:
    emails = [e for e in events if e.kind == "email"]
    inbound = [e for e in emails if e.inbound]
    outbound = [e for e in emails if not e.inbound]
    return {"last_inbound_at": inbound[-1].occurred_at if inbound else None,
            "last_inbound_from_person_id": person_for(snap, spec["deal"], inbound[-1]) if inbound else None,
            "last_outbound_at": outbound[-1].occurred_at if outbound else None,
            "stated_timing": spec.get("stated_timing")}


def commitments(snap: Snapshot, spec: dict[str, Any], events: list[Event]) -> list[dict[str, Any]]:
    people = correspondents(snap, events)
    owners = {"rep": rep_person(snap, spec["deal"]), "customer": uid("person", people[0]["Id"]) if people else None,
              None: None}
    return [{"text": text, "owner_person_id": owners[owner], "due_at": due, "status": status}
            for text, owner, due, status in spec.get("commitments", [])]


def candidate(snap: Snapshot, spec: dict[str, Any], now: str) -> dict[str, Any]:
    cand, deal = spec["cand"], spec["deal"]
    action = cand["action"]
    refs = []
    for sf_id, quote in cand.get("refs", []):
        event = snap.event(deal, sf_id)
        if event.occurred_at > now:
            raise SpecError(f"{spec['id']}: evidence {sf_id} is after now")
        if quote not in event.body:
            raise SpecError(f"{spec['id']}: quote not verbatim in {sf_id}: {quote!r}")
        ref = {"activity_id": uid(event.kind, sf_id), "quote": quote, "occurred_at": event.occurred_at}
        actor = person_for(snap, deal, event)
        refs.append({**ref, **({"speaker_person_id": actor} if actor else {})})
    recipients = [{"person_id": uid("person", c), "role": role}
                  for role, ids in (("to", cand.get("to", [])), ("cc", cand.get("cc", []))) for c in ids]
    channel = cand.get("channel", "email" if action == "send_email" else "none")
    body = cand.get("body", "")
    out: dict[str, Any] = {
        "proposed_action_type": action, "recipients": recipients,
        "finished_artifact": {"channel": channel, "subject": cand.get("subject"), "body": body,
                              **({"attachments": cand["attachments"]} if cand.get("attachments") else {})},
        "crm_next_step_intent": {"next_step": cand["next_step"], "due_at": cand.get("due"), "stage_change": None},
        "reason": cand["reason"], "evidence_refs": refs,
        "knowledge_refs_used": [KNOWLEDGE[k]["id"] for k in cand.get("kn", [])],
        "wait_until": cand.get("wait_until")}
    return out


def best_action(spec: dict[str, Any]) -> dict[str, Any]:
    best = spec["best"]
    out: dict[str, Any] = {"action": best["action"], "why": best["why"]}
    recipients = [{"person_id": uid("person", c), "role": role}
                  for role, ids in (("to", best.get("to", [])), ("cc", best.get("cc", []))) for c in ids]
    if recipients:
        out["recipients"] = recipients
    if best.get("channel"):
        out["channel"] = best["channel"]
    if best.get("wait_until"):
        out["wait_until"] = best["wait_until"]
    return out


def build_skeleton(snap: Snapshot, spec: dict[str, Any]) -> dict[str, Any]:
    deal = spec["deal"]
    trig = snap.event(deal, spec["trigger"])
    now = decision_time(trig, spec.get("now"))
    if trig.occurred_at > now:
        raise SpecError(f"{spec['id']}: trigger after now")
    events = visible(snap, deal, now)
    state, provenance = build_state(snap, spec, events, now)
    support_ids = [(i, lim) for i, lim in spec.get("support", []) if i != spec["trigger"]]
    support = []
    for sf_id, limit in support_ids:
        event = snap.event(deal, sf_id)
        if event.occurred_at > now:
            raise SpecError(f"{spec['id']}: support {sf_id} is after now")
        support.append(activity(snap, deal, event, limit or SUPPORT_LIMIT))
    support.sort(key=lambda a: (a["occurred_at"], a["event_file"]))
    candidate_out = candidate(snap, spec, now)
    shown = {a["event_file"].rsplit(":", 1)[1]: a["text"] for a in [activity(snap, deal, trig, TRIGGER_LIMIT), *support]}
    for sf_id, quote in spec["cand"].get("refs", []):
        if quote and (sf_id not in shown or quote not in shown[sf_id]):
            raise SpecError(f"{spec['id']}: the quoted evidence from {sf_id} is not in the excerpt shown in the context (CI cannot check it)")
    summary, material, signals = spec["recent"]
    knowledge = [KNOWLEDGE[k] for k in spec.get("knowledge", [])]
    return {
        "id": spec["id"], "origin": "crmarena", "case_type": spec["type"], "title": spec["title"],
        "difficulty": spec["difficulty"],
        "based_on": {"kind": "crmarena_moment", "account_id": snap.opportunities[deal]["AccountId"], "deal_id": deal,
                     "trigger_kind": {"email": "inbound_email" if trig.inbound else "outbound_email",
                                      "task": "task_due"}[trig.kind],
                     "decision_at": now, "cutoff": CUTOFF, "deal_group": "current",
                     "events_before_decision": len(events)},
        "source": {"dataset": "CRMArena-Pro B2B Salesforce org (Salesforce AI Research)",
                   "licence": "CC BY-NC 4.0 (non-commercial use only)", "attribution": ATTRIBUTION,
                   "snapshot_manifest_sha256": snap.manifest_sha256, "generator": GENERATOR},
        "context": {
            "now": now, "state": state,
            "recent_changes": {"summary": summary, "material_diff_fields": list(material), "signals": list(signals)},
            "trigger": activity(snap, deal, trig, TRIGGER_LIMIT), "supporting_activities": support,
            "commitments": commitments(snap, spec, events), "timeline_facts": timeline(snap, events, spec),
            "offered_knowledge": knowledge, "state_provenance": provenance},
        "candidate_action": candidate_out, "expected_best_action": best_action(spec),
        "evidence_gap": spec.get("gap"), "notes": spec.get("notes", ""),
    }
