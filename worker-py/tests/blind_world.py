"""Primitives, evidence kits and rendering for the blind transition gold (authored by a separate agent from the pitch alone).
See transition_gold_blind.py."""
from __future__ import annotations

import copy
import random
import sys
import uuid
from datetime import timedelta
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

_RNG = random.Random(20261003)


def _det_uuid4() -> uuid.UUID:
    return uuid.UUID(int=_RNG.getrandbits(128), version=4)


uuid.uuid4 = _det_uuid4  # builders_public calls uuid.uuid4(); make the output reproducible

import blind_builders as bp  # noqa: E402

at, ts = bp.at, bp.ts


def nid() -> str:
    return str(uuid.uuid4())


ACCOUNT = "0acc0000-0000-4000-8000-000000000001"
CHAMPION = bp.CHAMPION          # pre-reorg champion
OWNER2 = bp.OTHER               # existing customer-side user who later becomes the new owner/champion
BU_LEAD = "0b0e0000-0000-4000-8000-000000000031"   # lead from another business unit
EXEC = "0b0e0000-0000-4000-8000-000000000041"      # long-standing economic buyer
NEWCOMER = "0b0e0000-0000-4000-8000-000000000051"  # unidentified new participant
SECURITY = "0b0e0000-0000-4000-8000-000000000061"  # security reviewer
SELLER_AE2 = "05e10000-0000-4000-8000-000000000072"  # seller-side account executive (our side)

EXTRACTOR = {
    "first_party_ai": "llm:extractor@extract-v5", "first_party_record": "rule:activity_record@1",
    "crm_explicit": "rule:crm_field@1", "third_party": "rule:enrichment_import@1", "human_approved": "human:review_queue",
}
EVENT_SIGNALS = {
    "new_stakeholder_entered", "champion_weakened", "champion_reactivated", "champion_delegated", "blocker_resolved",
    "pricing_interest", "expansion_interest", "customer_replied", "meeting_accepted", "product_usage_increased",
    "stage_regressed", "stage_advanced",
}
EVENT_WINDOW = timedelta(days=14)
SCALARS = ["stage", "health", "owner", "motion", "champion", "champion_status", "economic_buyer", "decision_process",
           "next_milestone", "next_meeting", "relationship_risk", "product_use_case", "commercial_issue", "summary"]
LISTS = ["blockers", "objections", "decision_criteria", "current_commitments"]
NAMES = {CHAMPION: "Champion", OWNER2: "Operations lead", BU_LEAD: "Regional finance lead", EXEC: "Finance executive",
         NEWCOMER: "New participant", SECURITY: "Security reviewer"}


# ---------------------------------------------------------------- primitives
def mk_claim(path, occurred, value, quote, subject=None, standing="first_party_ai", speaker=None, status="active"):
    c = bp.claim(path, occurred, subject, standing)
    c.update({"account_id": ACCOUNT, "value": value, "confidence": 0.9, "source_activity_id": nid(),
              "extractor": EXTRACTOR[standing], "evidence_quote": quote, "speaker_person_id": speaker, "status": status})
    return c


def ev(c):
    return [{"activity_id": c["source_activity_id"], "claim_id": c["id"], "occurred_at": c["occurred_at"]}]


def unknown_field():
    return {"value": "unknown", "known": False, "winning_claim_id": None, "standing": None, "as_of": None,
            "evidence_refs": []}


def empty_list():
    return {"value": [], "known": False, "winning_claim_id": None, "standing": None, "as_of": None, "evidence_refs": []}


def scalar(c, conflicts=None):
    f = {"value": c["value"], "known": True, "winning_claim_id": c["id"], "standing": c["standing"], "confidence": 0.9,
         "as_of": c["occurred_at"], "evidence_refs": ev(c)}
    if conflicts:
        f["conflicts"] = [{"claim_id": x["id"], "standing": x["standing"], "reason": reason} for x, reason in conflicts]
        f["competing_claim_ids"] = [x["id"] for x, _ in conflicts]
    return f


def derived(occurred, activity_id):
    return {"value": occurred, "known": True, "derived": True, "winning_claim_id": None, "standing": None,
            "as_of": occurred, "evidence_refs": [{"activity_id": activity_id, "occurred_at": occurred}]}


def derived_unknown():
    return {"value": "unknown", "known": False, "derived": True, "winning_claim_id": None, "standing": None,
            "as_of": None, "evidence_refs": []}


class World:
    """Cumulative account picture: claims, signals, AccountState fields, buying group."""

    def __init__(self):
        self.claims, self.signals, self.fields, self.members, self.lists = [], [], {}, {}, {}

    def clone(self):
        return copy.deepcopy(self)

    def add(self, c):
        self.claims.append(c)
        return c

    def sig(self, kind, created, subject=None, claim=None, details=None, is_open=None):
        s = bp.sig(kind, created, True, subject)
        s["account_id"] = ACCOUNT
        if claim is not None:
            s["subject_claim_id"] = claim["id"]
            s["evidence_refs"] = ev(claim)
        else:
            s["evidence_refs"] = [{"activity_id": nid(), "occurred_at": created}]
        if details:
            s["details"] = details
        s["_open"] = is_open
        self.signals.append(s)
        return s

    def list_item(self, field, c, text, status="open", kind="other", subject=None, due=None, owner=None):
        self.lists.setdefault(field, []).append({"text": text, "claim_id": c["id"], "status": status, "kind": kind,
                                                 "subject_person_id": subject, "due_at": due, "owner_person_id": owner,
                                                 "evidence_refs": ev(c), "_standing": c["standing"]})

    def member(self, pid, roles, status, occurred, c=None, title=None, tenure=None):
        m = {"person_id": pid, "display_name": NAMES[pid], "title": title, "roles": roles, "status": status,
             "last_engaged_at": occurred, "evidence_refs": ev(c) if c else [{"activity_id": nid(), "occurred_at": occurred}]}
        if tenure:
            m["tenure"] = tenure
        self.members[pid] = m


# ---------------------------------------------------------------- evidence kits
def baseline(eb=True, motion="renewal"):
    w = World()
    ch = w.add(mk_claim("champion", "2026-02-01T00:00:00Z", CHAMPION, None, CHAMPION, "crm_explicit"))
    st = w.add(mk_claim("champion_status", "2026-02-01T00:00:00Z", "active", "Happy to keep driving this on our side.",
                        CHAMPION, speaker=CHAMPION))
    w.fields.update(champion=scalar(ch), champion_status=scalar(st), champion_since=derived(ch["occurred_at"], ch["source_activity_id"]))
    w.member(CHAMPION, ["champion"], "active", "2026-02-01T00:00:00Z", ch, "Operations manager", "permanent")
    u = w.add(mk_claim("buying_group.member", "2026-02-01T00:00:00Z", "Day-to-day user of the product",
                       "Looping in my colleague who uses it every day.", OWNER2, speaker=CHAMPION))
    w.member(OWNER2, ["user"], "active", "2026-02-01T00:00:00Z", u, "Operations analyst", "permanent")
    mo = w.add(mk_claim("motion", "2026-02-01T00:00:00Z", motion, None, standing="crm_explicit"))
    sg = w.add(mk_claim("stage", "2026-02-01T00:00:00Z", "active_customer", None, standing="crm_explicit"))
    rk = w.add(mk_claim("relationship_risk", "2026-02-01T00:00:00Z", "low", "Everything is running smoothly.", speaker=CHAMPION))
    w.fields.update(motion=scalar(mo), stage=scalar(sg), relationship_risk=scalar(rk))
    if eb:
        e = w.add(mk_claim("economic_buyer", "2026-03-01T00:00:00Z", EXEC, None, EXEC, "crm_explicit"))
        w.fields["economic_buyer"] = scalar(e)
        w.member(EXEC, ["economic_buyer"], "active", "2026-03-01T00:00:00Z", e, "VP Finance", "permanent")
    return w


def kit_champion_role_change(w, t, person=CHAMPION, standing="first_party_ai", with_stage=True):
    """Customer states the champion moved / changed responsibility; champion departs the role; motion paused."""
    oc = w.add(mk_claim("org_change", t, {"kind": "role_change", "summary": "The champion moved to lead another group and no longer owns this rollout"},
                        "I'm moving over to lead the platform group, so I won't be owning this rollout going forward.",
                        person, standing, speaker=person))
    w.list_item("org_changes", oc, oc["value"]["summary"], kind="relationship", subject=person)
    st = w.add(mk_claim("champion_status", t, "departed", "I won't be owning this rollout going forward.", person, standing, speaker=person))
    w.fields["champion_status"] = scalar(st)
    w.sig("champion_weakened", t, person, st)
    m = w.members[person]
    m.update(status="departed", last_engaged_at=t)
    if with_stage:
        sg = w.add(mk_claim("stage", t, "on_hold", None, standing="crm_explicit"))
        w.fields["stage"] = scalar(sg)
        w.sig("stage_regressed", t, None, sg)
    return oc


def kit_restructure(w, t, pause=True, kind="reorganisation", summary="Operations is being reorganised under a new VP next month",
                    quote="Ops is being reorganised under a new VP next month, so let's pause the rollout discussion until the new structure settles."):
    oc = w.add(mk_claim("org_change", t, {"kind": kind, "summary": summary}, quote, None, speaker=CHAMPION))
    w.list_item("org_changes", oc, summary, kind="relationship")
    if pause:
        t2 = at(t, 1)
        sg = w.add(mk_claim("stage", t2, "on_hold", None, standing="crm_explicit"))
        w.fields["stage"] = scalar(sg)
        w.sig("stage_regressed", t2, None, sg)
    return oc


def kit_champion_weakening(w, t):
    st = w.add(mk_claim("champion_status", t, "weakening", "Sorry for the slow replies, things are hectic this quarter.",
                        CHAMPION, speaker=CHAMPION))
    w.fields["champion_status"] = scalar(st)
    w.sig("champion_weakened", t, CHAMPION, st)
    w.members[CHAMPION].update(status="weakening", last_engaged_at=t)


def kit_newcomer(w, t):
    c = w.add(mk_claim("buying_group.member", t, "New participant added to the thread; role not stated", None,
                       NEWCOMER, "first_party_record"))
    w.sig("new_stakeholder_entered", t, NEWCOMER, c)
    w.member(NEWCOMER, ["unknown"], "new", t, c)


def kit_owner(w, t, person=OWNER2, tenure="permanent", title="Director of Operations", standing="first_party_ai"):
    ch = w.add(mk_claim("champion", t, person, "I'm taking over ownership of the rollout from here.", person, standing, speaker=person))
    st = w.add(mk_claim("champion_status", t, "active", "I'm taking over ownership of the rollout from here.", person, standing, speaker=person))
    rq = "I'm covering as acting director until a permanent head is hired." if tenure == "interim" else f"I've been appointed {title}."
    rl = w.add(mk_claim("stakeholder_role", t, title, rq, person, standing, speaker=person))
    w.fields.update(champion=scalar(ch), champion_status=scalar(st), champion_since=derived(t, ch["source_activity_id"]))
    roles = ["champion"] if person != BU_LEAD else ["champion", "influencer"]
    w.member(person, roles, "active", t, rl, title, tenure)
    return ch


def kit_stakeholder(w, t, person=BU_LEAD, standing="first_party_ai", title="Head of Regional Finance"):
    c = w.add(mk_claim("buying_group.member", t, f"{title} joined the rollout discussion for the regional finance business unit",
                       "Adding our regional finance lead, who wants to see how this would work for their team.", person, standing, speaker=OWNER2))
    w.add(mk_claim("stakeholder_role", t, title, "I run regional finance.", person, standing, speaker=person))
    w.sig("new_stakeholder_entered", t, person, c)
    w.member(person, ["influencer", "user"], "active", t, c, title, "permanent")
    return c


def kit_need(w, t, speaker=BU_LEAD, standing="first_party_ai", status="active",
             text="Roll the product out to the regional finance team, about 40 more seats"):
    c = w.add(mk_claim("expansion_need", t, text, "We'd like to roll this out to the regional finance team as well, roughly 40 more seats.",
                       None, standing, speaker=speaker, status=status))
    if status == "active":
        w.list_item("expansion_needs", c, text)
        w.sig("expansion_interest", t, speaker, c)
    return c


def kit_value(w, t, standing="first_party_ai", speaker=OWNER2):
    text = "Month-end close preparation went from five days to three"
    c = w.add(mk_claim("business_value", t, text, "Month-end close prep is down from five days to three since we started.", None, standing, speaker=speaker))
    w.list_item("business_value", c, text)
    w.sig("product_usage_increased", t, None, c)
    return c


def kit_commercial(w, t, dp_standing="first_party_ai", owner=OWNER2):
    cm = w.add(mk_claim("commitment", t, "Send the order form for 40 additional seats",
                        "Please send over the order form for the 40 extra seats and we'll start procurement.", None, speaker=owner))
    w.list_item("current_commitments", cm, cm["value"], kind="commercial", due=at(t, 14), owner=owner)
    dp = w.add(mk_claim("decision_process", t, "VP Finance signs off, then procurement review",
                        "VP Finance signs off and then it goes through procurement.", None, dp_standing, speaker=owner))
    mo = w.add(mk_claim("motion", t, "expansion", None, standing="crm_explicit"))
    sg = w.add(mk_claim("stage", t, "proposal", None, standing="crm_explicit"))
    w.fields.update(decision_process=scalar(dp), motion=scalar(mo), stage=scalar(sg))
    w.sig("pricing_interest", t, owner, cm)
    w.sig("stage_advanced", t, None, sg)
    return cm, dp


def kit_next_step(w, t):
    c = w.add(mk_claim("next_meeting", t, f"Rollout scoping call on {at(t, 10)[:10]}",
                       "Let's set up a call to scope what a rollout would look like.", None, speaker=OWNER2))
    w.fields["next_meeting"] = scalar(c)
    w.sig("meeting_accepted", t, OWNER2, c)


def reorg_world(confirmed_at, bg_at, eb=True):
    w = baseline(eb=eb)
    kit_champion_role_change(w, bg_at)
    return w, {"value": "REORG", "transition_id": nid(), "confirmed_at": confirmed_at}


def bundle(w, owner_at=None, stake_at=None, need_at=None, value_at=None, comm_at=None, next_at=None,
           tenure="permanent", need_speaker=BU_LEAD):
    if owner_at:
        kit_owner(w, owner_at, tenure=tenure, title="Acting Director of Operations" if tenure == "interim" else "Director of Operations")
    if stake_at:
        kit_stakeholder(w, stake_at)
    if need_at:
        kit_need(w, need_at, speaker=need_speaker if stake_at else OWNER2)
    if value_at:
        kit_value(w, value_at)
    if comm_at:
        kit_commercial(w, comm_at)
    if next_at:
        kit_next_step(w, next_at)
    return w


# ---------------------------------------------------------------- rendering
def render(w, now, rel, version):
    snap = w.clone()
    for c in snap.claims:
        assert ts(c["occurred_at"]) <= ts(now), (c["field_path"], c["occurred_at"], now)
    signals = []
    for s in snap.signals:
        assert ts(s["created_at"]) <= ts(now), (s["signal_type"], s["created_at"], now)
        forced = s.pop("_open")
        if forced is not None:
            s["open"] = forced
        elif s["signal_type"] in EVENT_SIGNALS:
            s["open"] = ts(now) - ts(s["created_at"]) < EVENT_WINDOW
        else:
            s["open"] = True
        # ADR-0015 (signal.v1.json): occurred_at is the world time; an EVENT signal carries its window's end. Mechanical
        # contract migration only: no label or scenario content changes.
        s["occurred_at"] = s["created_at"]
        s["expires_at"] = (ts(s["created_at"]) + EVENT_WINDOW).strftime("%Y-%m-%dT%H:%M:%SZ") if s["signal_type"] in EVENT_SIGNALS else None
        signals.append(s)
    st = bp.state(rel)  # builder defaults are fully overwritten below
    fields = {k: unknown_field() for k in SCALARS}
    fields.update({k: empty_list() for k in LISTS})
    fields.update(last_customer_interaction=derived_unknown(), last_meaningful_change=derived_unknown())
    fields.update(snap.fields)
    for name, items in snap.lists.items():
        standings = {i.pop("_standing") for i in items}
        latest = max(items, key=lambda i: i["evidence_refs"][0]["occurred_at"])
        fields[name] = {"value": items, "known": True, "winning_claim_id": None,
                        "standing": "third_party" if standings == {"third_party"} else sorted(standings - {"third_party"})[0],
                        "as_of": latest["evidence_refs"][0]["occurred_at"],
                        "evidence_refs": [r for i in items for r in i["evidence_refs"]]}
    customer = [c for c in snap.claims if c["standing"] != "third_party" and c["speaker_person_id"]]
    if customer:
        last = max(customer, key=lambda c: c["occurred_at"])
        fields["last_customer_interaction"] = derived(last["occurred_at"], last["source_activity_id"])
    if snap.claims:
        last = max(snap.claims, key=lambda c: c["occurred_at"])
        fields["last_meaningful_change"] = derived(last["occurred_at"], last["source_activity_id"])
    st["fields"] = fields
    st["buying_group"] = list(snap.members.values())
    st.update(account_id=ACCOUNT, version=version, as_of=max(c["occurred_at"] for c in snap.claims),
              computed_at=at(now, seconds=3))
    return {"state": st, "signals": signals, "claims": snap.claims, "now": now, "computed_at": at(now, seconds=3),
            "open_transition": None}


def gold(status, after, to_state=None, closed=False):
    g = {"status": status, "relationship_state_after": after, "closed": closed}
    if status is not None:
        g["to_state"] = None if status == "UNRESOLVED" else to_state
    return g


SCENARIOS: list[dict] = []


def scenario(sid, source, rationale, initial_rel, steps, contested=None):
    """steps: list of (label, world, now, gold). Chains relationship_state through CONFIRMED steps."""
    rel = initial_rel
    out = []
    for i, (label, w, now, g) in enumerate(steps):
        out.append({"label": label, "input": render(w, now, rel, version=10 + i), "gold": g})
        if g["status"] == "CONFIRMED":
            rel = {"value": g["to_state"], "transition_id": nid(), "confirmed_at": at(now, seconds=3)}
    s = {"id": sid, "slice": "blind", "source": source, "rationale": rationale, "initial_state": initial_rel["value"]}
    if contested:
        s["contested"] = contested
    s["steps"] = out
    SCENARIOS.append(s)


UNK = bp.UNKNOWN_STATE
S3 = "pitch section 3 (Reorg -> expansion should be earned by evidence)"
S3A = "pitch section 3, State A (REORG / relationship disruption)"
S2 = "pitch section 2 (transition statuses)"


def one(sid, source, rationale, rel, w, now, g, label="single evaluation", contested=None):
    scenario(sid, source, rationale, rel, [(label, w, now, g)], contested)


# ================================================================ A. pitch bundles, REORG -> EXPANSION
R_CONF, R_BG = "2026-07-01T00:00:00Z", "2026-06-28T00:00:00Z"
