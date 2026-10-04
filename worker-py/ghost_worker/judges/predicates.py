"""Cheap, deterministic routing signals over a JudgeContext (HAR-97 §7: route from workflow type + account
state + trigger + proposed action). Rubrics name these predicates; they never call a model."""
from __future__ import annotations

import re
from collections.abc import Callable, Mapping
from datetime import datetime, timedelta
from types import MappingProxyType
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from .context import JudgeContext

QUIET = frozenset({"wait", "no_action"})
EXTERNAL = frozenset({"send_email", "schedule_meeting", "share_document"})
SILENCE = timedelta(days=5)

_COMMERCIAL = re.compile(
    r"\b(pric\w*|discount\w*|order form|contract\w*|signed (the|an?|our)|signature\w*|seats?|"
    r"licen[cs]\w*|upgrade\w*|upsell\w*|proposal\w*|quote|purchase\w*|invoice\w*|"
    r"paperwork|roi)\b", re.IGNORECASE)
# "call" / "session" alone are not asks ("call transcript", "on a call with legal"): a meeting ask names a meeting,
# an invite, or an act of booking one.
_MEETING = re.compile(
    r"(\bcalendar\b|\binvite\w*|\bwalk-?through\b|\bworkshop\b|\btime to meet\b|\breview date\b|\b(30|45|60)-minute\b|"
    r"\bmeeting\b|\b(schedul|book|set up|arrang|propos|hold|join)\w*( an?| the| our| us for)?( \w+)?"
    r" (call|session|demo|sync)\b)", re.IGNORECASE)
# "hours" / "%" alone are not value claims ("within 24 hours", "100% agree"): a figure must be tied to an outcome.
_VALUE = re.compile(
    r"(\broi\b|\bsav(e|es|ing|ings)\b|\bpayback\b|\befficien\w*|\bcost (saving|reduction)\w*|\b\d+ hours (saved|a week|per)\b|"
    r"\d(\.\d+)? ?% (faster|lower|fewer|less|more|reduction|improvement|increase|savings?))", re.IGNORECASE)
_EARLY_STAGE = re.compile(r"discovery|qualif|prospect|intro", re.IGNORECASE)
# A promise or hand-off the customer states in the thread, and what a draft says about its own commitments.
_THREAD_PROMISE = re.compile(r"(final sign-?off|bring me (back )?in|loop \w+ (back )?in|get back to|promis\w+|"
                             r"\bcommit(ted|ment)?\b)", re.IGNORECASE)
_DRAFT_PROMISE = re.compile(r"(follow up|get back to|promis\w+|\bcommit(ted|ment)?\b|\bowe\w*)", re.IGNORECASE)
_WILL = re.compile(r"\b(i|we)(?:['\u2019]ll| will)\b", re.IGNORECASE)
# The customer asked for a calendar invite: answering with an email that asks which day is a channel miss.
_INVITE_REQUEST = re.compile(r"(\binvite\b|\bcalendar\b)", re.IGNORECASE)
_RISK_BLOCKER = re.compile(
    r"(security|complian\w*|\bphi\b|hipaa|\bbaa\b|privacy|gdpr|residency|legal|incident|outage|breach|soc ?2|pen-?test)",
    re.IGNORECASE)
STATE_FIELDS = frozenset({"stage", "champion", "champion_status", "economic_buyer"})

ENGAGEMENT_SIGNALS = frozenset({"customer_went_silent", "champion_reactivated", "meeting_accepted", "stage_regressed",
                                "stage_advanced", "champion_weakened"})
RISK_SIGNALS = frozenset({"support_risk_spike", "stage_regressed"})  # a security blocker is open_blocker's
CHAMPION_SIGNALS = frozenset({"champion_weakened", "champion_delegated", "champion_reactivated"})
EXCEPTION_STATUSES = frozenset({"delegated", "departed", "inactive"})
UNKNOWN = "unknown"


def _action_text(ctx: JudgeContext) -> str:
    action = ctx.candidate_action
    artifact = action.get("finished_artifact", {})
    parts = [artifact.get("subject") or "", artifact.get("body") or "",
             action.get("crm_next_step_intent", {}).get("next_step") or ""]
    return "\n".join(parts)


def _customer_text(ctx: JudgeContext) -> str:
    """What the customer side said: the trigger and the supporting activities."""
    return "\n".join(str(a.get("text") or "") for a in (ctx.trigger, *ctx.supporting_activities))


def _signals(ctx: JudgeContext) -> frozenset[str]:
    return frozenset(ctx.recent_changes.get("signals", ()))


def _parse(ts: Any) -> datetime | None:
    if not isinstance(ts, str):
        return None
    try:
        return datetime.fromisoformat(ts.replace("Z", "+00:00"))
    except ValueError:
        return None


def _is_unknown(value: Any) -> bool:
    return value is None or value == UNKNOWN


def _items(value: Any) -> tuple[Mapping[str, Any], ...]:
    """The mapping items of a list-valued state field ('unknown' or a missing field has none)."""
    return tuple(v for v in value if isinstance(v, Mapping)) if isinstance(value, tuple) else ()


def has_recipients(ctx: JudgeContext) -> bool:
    return bool(ctx.candidate_action.get("recipients"))


def written_message(ctx: JudgeContext) -> bool:
    artifact = ctx.candidate_action.get("finished_artifact", {})
    return artifact.get("channel") in {"email", "slack"} and bool((artifact.get("body") or "").strip())


def commercial_ask(ctx: JudgeContext) -> bool:
    return ctx.action_type in EXTERNAL and bool(_COMMERCIAL.search(_action_text(ctx)))


def meeting_ask(ctx: JudgeContext) -> bool:
    return ctx.action_type == "schedule_meeting" or (
        ctx.action_type in EXTERNAL and bool(_MEETING.search(_action_text(ctx))))


def value_claim(ctx: JudgeContext) -> bool:
    return bool(_VALUE.search(_action_text(ctx)))


def early_stage(ctx: JudgeContext) -> bool:
    return ctx.fields.get("motion") == "new_business" or bool(_EARLY_STAGE.search(str(ctx.fields.get("stage", ""))))


def expansion_motion(ctx: JudgeContext) -> bool:
    return ctx.fields.get("motion") in {"expansion", "renewal"} or "expansion_interest" in _signals(ctx)


def has_offered_knowledge(ctx: JudgeContext) -> bool:
    return bool(ctx.offered_knowledge)


def has_rep_profile(ctx: JudgeContext) -> bool:
    return ctx.rep_profile is not None


def has_open_commitments(ctx: JudgeContext) -> bool:
    """Our side owes something open or overdue (a commitment whose owner is outside the buying group)."""
    members = {m.get("person_id") for m in ctx.state.get("buying_group", ())}
    listed = _items((*ctx.commitments, *_items(ctx.fields.get("current_commitments"))))
    return any(c.get("status") in {"open", "overdue"} and c.get("owner_person_id") not in members for c in listed)


def open_blocker(ctx: JudgeContext) -> bool:
    """The action pushes forward (a commercial or meeting ask) through an open blocker of the customer-risk kind
    (security, compliance/PHI, privacy, legal, incident): a customer-risk question even when the account's risk
    rating is low."""
    risk_blocked = any(b.get("status") == "open" and _RISK_BLOCKER.search(str(b.get("text") or ""))
                       for b in _items(ctx.fields.get("blockers")))
    return risk_blocked and (commercial_ask(ctx) or meeting_ask(ctx))


def risk_elevated(ctx: JudgeContext) -> bool:
    """Customer risk (not a deal gate): high relationship risk or a risk signal. A health rating alone is not an
    incident (it is at_risk on most accounts of the fixture world), and a security blocker is open_blocker's."""
    return ctx.fields.get("relationship_risk") == "high" or bool(_signals(ctx) & RISK_SIGNALS)


def channel_choice(ctx: JudgeContext) -> bool:
    """The channel is a real choice: anything but a plain email reply, or an email answering a customer request
    for a calendar invite (an email asking which day, instead of sending the invite, is a channel miss)."""
    channel = ctx.candidate_action.get("finished_artifact", {}).get("channel")
    return ctx.action_type != "send_email" or channel != "email" or bool(_INVITE_REQUEST.search(_customer_text(ctx)))


def proactive_outreach(ctx: JudgeContext) -> bool:
    """Nothing the customer said triggered this action (a rep-initiated nudge, third-party enrichment)."""
    return not ctx.trigger.get("text")


def commitment_in_play(ctx: JudgeContext) -> bool:
    """A commitment is being touched: it is overdue, it is mentioned in the thread or the draft, or the
    draft promises something while a commitment already stands fulfilled (a re-promise)."""
    mentioned = bool(_THREAD_PROMISE.search(_customer_text(ctx)) or _DRAFT_PROMISE.search(_action_text(ctx)))
    listed = _items((*ctx.commitments, *_items(ctx.fields.get("current_commitments"))))
    re_promise = any(c.get("status") == "fulfilled" for c in listed) and bool(_WILL.search(_action_text(ctx)))
    return mentioned or re_promise or "commitment_overdue" in _signals(ctx)


def _recipient_ids(ctx: JudgeContext) -> set[Any]:
    return {r.get("person_id") for r in ctx.candidate_action.get("recipients", ())}


def breadth_in_question(ctx: JudgeContext) -> bool:
    """How many people are addressed is a question: several recipients, or a commercial ask to one person while
    coverage gaps remain (single-threading)."""
    recipients = _recipient_ids(ctx)
    single_threaded_ask = len(recipients) == 1 and bool(ctx.state.get("coverage_gaps")) and commercial_ask(ctx)
    return len(recipients) > 1 or single_threaded_ask


def addressee_in_question(ctx: JudgeContext) -> bool:
    """Who is addressed is a question: the writer is left out, someone is outside the buying group, or a new
    stakeholder just entered."""
    ids = _recipient_ids(ctx)
    members = {m.get("person_id") for m in ctx.state.get("buying_group", ())}
    return bool(ids) and (ctx.trigger.get("actor_person_id") not in ids or bool(ids - members)
                          or "new_stakeholder_entered" in _signals(ctx))


def commitment_pending(ctx: JudgeContext) -> bool:
    """We owe something open and the thread has gone quiet, a reply is pending, nothing triggered this action or
    the action is to wait: the action may ignore it."""
    return has_open_commitments(ctx) and (customer_quiet(ctx) or awaiting_reply(ctx) or proactive_outreach(ctx)
                                          or quiet_action(ctx))


def champion_off_thread(ctx: JudgeContext) -> bool:
    """A known champion is not among the recipients (bypass or hand-off)."""
    champion = ctx.fields.get("champion")
    return not _is_unknown(champion) and champion not in _recipient_ids(ctx)


def state_moved(ctx: JudgeContext) -> bool:
    """The deal state moved (stage, health, blockers, champion, buyer or process changed, or a field is
    contradicted) or nothing the customer said triggered the action."""
    moved = STATE_FIELDS & set(ctx.recent_changes.get("material_diff_fields", ()))
    return bool(moved) or "field_contradicted" in _signals(ctx) or proactive_outreach(ctx)


def champion_known(ctx: JudgeContext) -> bool:
    return not _is_unknown(ctx.fields.get("champion"))


def champion_in_question(ctx: JudgeContext) -> bool:
    return (not champion_known(ctx) or ctx.fields.get("champion_status") != "active"
            or bool(_signals(ctx) & CHAMPION_SIGNALS))


def exception_in_state(ctx: JudgeContext) -> bool:
    members = ctx.state.get("buying_group", ())
    return (ctx.fields.get("champion_status") in EXCEPTION_STATUSES
            or any(m.get("status") in EXCEPTION_STATUSES or m.get("delegated_to_person_id") for m in members)
            or "champion_delegated" in _signals(ctx))


def eb_in_recipients(ctx: JudgeContext) -> bool:
    eb = ctx.fields.get("economic_buyer")
    return not _is_unknown(eb) and any(r.get("person_id") == eb for r in ctx.candidate_action.get("recipients", ()))


def process_gap(ctx: JudgeContext) -> bool:
    gaps = frozenset(ctx.state.get("coverage_gaps", ()))
    return bool(gaps & {"security", "legal", "procurement"}) or _is_unknown(
        ctx.fields.get("decision_process"))


def quiet_action(ctx: JudgeContext) -> bool:
    return ctx.action_type in QUIET


def awaiting_reply(ctx: JudgeContext) -> bool:
    """Our last outbound is newer than the customer's last inbound: another touch would be a nudge."""
    facts = ctx.timeline_facts
    outbound, inbound = _parse(facts.get("last_outbound_at")), _parse(facts.get("last_inbound_at"))
    return outbound is not None and (inbound is None or outbound > inbound)


def customer_quiet(ctx: JudgeContext) -> bool:
    """The customer has gone quiet: no inbound for SILENCE, or the customer_went_silent signal."""
    inbound, now = _parse(ctx.timeline_facts.get("last_inbound_at")), _parse(ctx.now)
    silent = inbound is not None and now is not None and now - inbound >= SILENCE
    return silent or "customer_went_silent" in _signals(ctx)


def engagement_shift(ctx: JudgeContext) -> bool:
    return customer_quiet(ctx) or bool(_signals(ctx) & ENGAGEMENT_SIGNALS)


def evidence_thin(ctx: JudgeContext) -> bool:
    """Conflicting sources, an unknown stage or decision process, or an addressee outside the buying group (an unknown
    approver on a commercial ask is the economic-buyer judge's question, not a reason to doubt the evidence)."""
    members = {m.get("person_id") for m in ctx.state.get("buying_group", ())}
    outsider = any(r.get("person_id") not in members for r in ctx.candidate_action.get("recipients", ()))
    unknown_fields = any(_is_unknown(ctx.fields.get(f)) for f in ("decision_process", "stage"))
    conflicting = bool(ctx.state.get("conflicts")) or "field_contradicted" in _signals(ctx)
    return conflicting or outsider or unknown_fields


PREDICATES: Mapping[str, Callable[[JudgeContext], bool]] = MappingProxyType({
    f.__name__: f for f in (
        has_recipients, written_message, channel_choice, commercial_ask, meeting_ask, value_claim, early_stage, expansion_motion, proactive_outreach, customer_quiet, commitment_in_play, commitment_pending, breadth_in_question, addressee_in_question,
        champion_off_thread, state_moved,
        has_offered_knowledge, has_rep_profile, open_blocker, risk_elevated, champion_known,
        champion_in_question, exception_in_state, eb_in_recipients, process_gap, quiet_action, awaiting_reply,
        engagement_shift, evidence_thin)
})
