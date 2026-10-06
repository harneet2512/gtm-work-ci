"""The four hidden rules, realised as SELLER actions (spec §d2; user decision 2026-10-02).

Each is a choice the seller (and so the agent) makes, drawn from the deal's policy stream
independently of health, so the planted effect is identifiable and A/B/C can score the agent's pick:
  P1 syn1_ops_contact_before_quote       write to an operations-title contact before Quote (recipients)
  P2 syn1_reprice_after_quote_pushback   after the buyer pushes back at Quote: re-price, or hold the price
                                          and make the value case (CTA / commercial step)
  P3 syn1_cc_colleague_on_technical_reply on the first reply to a technical-title buyer: cc a second seller
                                          colleague or not (recipients, internal cc)
  P4 syn1_second_quote_within_30d        at Quote, with another quote open at the account in the last 30 days:
                                          send a separate quote, or fold it into the open one (CRM update)
Both arms of every choice leave an observable seller record, so a learner sees the decision either way.
"""
from __future__ import annotations

from .state import H_CC, H_OPS, H_REPRICE, H_SECOND_QUOTE, State

PUSHBACK_REAL_EMAIL_GUARD_DAYS = 7.0  # no synthetic pushback near a real customer email (never contradict one)


def noise_cc(st: State) -> bool:
    """Neutral colleague cc on a synthetic seller email that is NOT P3's technical reply: same colleague pool and
    rendering, a matched share (cc_noise_share), no planted effect. Without it, 'has a cc' alone would mark P3
    (user decision 2026-10-03); with it, cc presence alone predicts nothing (tested in power_sim)."""
    return st.policy.uniform() < st.proc["cc_noise_share"]


def on_seller_email(st: State, t: float, recipients: tuple[str, ...]) -> None:
    """Any seller email (base or synthetic): P1 holds if it reaches an operations-title contact before Quote."""
    if st.stage < 2 and any((c := st.contact(r)) is not None and c.function == "operations" for r in recipients):
        st.hold(H_OPS, t)


def on_ops_note(st: State, t: float, contact: str) -> None:
    """P1's synthetic arm: a short seller note to an operations-title contact (planned by plan.py)."""
    if st.dormant:
        return
    st.record(t, "seller_email", to=contact, purpose="ops_note", cc_colleague=noise_cc(st))
    on_seller_email(st, t, (contact,))


def on_quote_entry(st: State, t: float) -> None:
    """At Quote: P2's buyer pushback (if no real email is near) and P4's separate-or-fold choice."""
    ps = st.policy
    pushback, delay, reprice = ps.uniform(), 1.0 + 9.0 * ps.uniform(), ps.uniform()
    separate = ps.uniform()
    if pushback < st.proc["pushback_at_quote_share"] and not st.real_email_near(t + delay, PUSHBACK_REAL_EMAIL_GUARD_DAYS):
        st.push(t + delay, "pushback", reprice < st.proc["reprice_share"])
    recent = [q for q in st.deal.account_quote_days if t - 30.0 < q < t]
    if recent:
        separate_now = separate < st.proc["separate_quote_share"]
        st.record(t, "quote_sent", separate=separate_now)
        if separate_now:
            st.hold(H_SECOND_QUOTE, t)


def on_pushback(st: State, t: float, reprice: bool) -> None:
    """P2: the buyer objects to the price; the seller re-prices (CRM Amount edit) or holds and argues value."""
    buyer = st.champion or next((c.id for c in st.deal.contacts if c.role == "economic_buyer"), None)
    if st.dormant or buyer is None:
        return
    st.record(t, "reply", contact=buyer, reply_kind="price_objection", timing_until=None)
    st.engaged.add(buyer)
    answer = t + 1.0
    if reprice:
        st.record(answer, "amount_edited", reason="pushback")
        st.hold(H_REPRICE, answer)
    else:
        st.record(answer, "seller_email", to=buyer, purpose="value_case", cc_colleague=noise_cc(st))


def on_buyer_email(st: State, t: float, sender: str) -> None:
    """P3: the first email from a technical-title buyer gets a seller reply that cc's a colleague or not."""
    who = st.contact(sender)
    if st.tech_answered or who is None or who.function != "technical" or st.dormant:
        return
    st.tech_answered = True
    cc, delay = st.policy.uniform() < st.proc["cc_colleague_share"], 0.1 + 1.4 * st.policy.uniform()
    st.push(t + delay, "tech_ack", sender, cc)


def on_tech_ack(st: State, t: float, sender: str, cc: bool) -> None:
    if st.dormant:
        return
    st.record(t, "seller_email", to=sender, purpose="technical_reply", cc_colleague=cc)
    if cc:
        st.hold(H_CC, t)
