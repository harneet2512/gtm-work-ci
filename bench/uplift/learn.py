"""Previous-deal learning: from observed seller decisions and their outcomes to knowledge candidates and evidence.

The rule that turns a hypothesis into a knowledge candidate is fixed here, before any run, and never tuned:
every catalog hypothesis (4 seller-action decision points, 3 controls) is tested on the previous deals with a
two-sided Fisher exact test of won-vs-lost between the deals that took the option and those that did not (each
option needs at least MIN_PER_OPTION deals), and the family is cut with Benjamini-Hochberg at q = PROMOTION_Q.
Controls that pass are promoted too and counted as false promotions: the learner is not told which are controls.

Only information that existed before the cutoff is used: a deal whose outcome falls on or after the cutoff, and any
decision or reaction on or after it, is excluded. The seller's recorded choice in each previous deal is the human
decision the knowledge is learned from.

Evidence given to the real lifecycle (knowledge.Record) for each promoted item, one unit per previous-deal episode in
which the RECOMMENDED option was chosen, whatever the outcome (no cherry-picking of winners):
  decision_episode  the episode;  business_outcome  closed_won / closed_lost at the close;
  customer_reaction  positive / negative from the buyer's next reply (when there is one before the cutoff).
No counterexample is recorded: a lost deal does not contradict a rule about odds, the statistical test carries the
uncertainty (p-value and counts in the guidance summary), and the lifecycle's ladder counts occurrences only, so a
status such as 'confirmed' is an upper bound that says nothing about the outcome denominator.
"""
from __future__ import annotations

import hashlib
import json
import math
import statistics
import uuid
from collections.abc import Sequence
from datetime import datetime, timezone

from . import catalog
from .observe import DealView, Obs, observe

PROMOTION_Q = 0.10
MIN_PER_OPTION = 20
NAMESPACE = uuid.UUID("5d1c6c0e-7b52-4f55-9f3a-0f6c1a9a7e11")
PROMOTION_RULE = (
    f"two-sided Fisher exact test of won vs lost between deals that took the option and those that did not, "
    f"each option with at least {MIN_PER_OPTION} previous deals; Benjamini-Hochberg across all {len(catalog.POINTS)} "
    f"catalog hypotheses at q = {PROMOTION_Q}; fixed before any run")


def det_uuid(*parts: str) -> str:
    return str(uuid.uuid5(NAMESPACE, "|".join(parts)))


def fisher_exact_p(a: int, b: int, c: int, d: int) -> float:
    """Two-sided exact p of the 2x2 table [[a, b], [c, d]] (sum of tables no more likely than the observed one)."""
    row1, row2, col1, n = a + b, c + d, a + c, a + b + c + d
    if n == 0:
        return 1.0
    denom = math.comb(n, col1)
    def prob(x: int) -> float:
        return math.comb(row1, x) * math.comb(row2, col1 - x) / denom
    lo, hi = max(0, col1 - row2), min(row1, col1)
    observed = prob(a)
    return min(1.0, sum(p for p in (prob(x) for x in range(lo, hi + 1)) if p <= observed * (1 + 1e-9)))


def benjamini_hochberg(pvalues: Sequence[float | None], q: float) -> list[bool]:
    """Which hypotheses survive BH at level q; untestable ones (None) never do."""
    idx = sorted((i for i, p in enumerate(pvalues) if p is not None), key=lambda i: pvalues[i])
    m = len(idx)
    cut = -1
    for rank, i in enumerate(idx, start=1):
        if pvalues[i] <= rank / m * q:
            cut = rank
    keep = [False] * len(pvalues)
    for rank, i in enumerate(idx, start=1):
        keep[i] = rank <= cut
    return keep


def _usable(views: Sequence[DealView], cutoff: datetime) -> tuple[list[DealView], int]:
    kept = [v for v in views if v.at(v.close_t) < cutoff]
    return kept, len(views) - len(kept)


def _observations(views: Sequence[DealView], cutoff: datetime) -> list[Obs]:
    out: list[Obs] = []
    by_view = {v.deal_id: v for v in views}
    for v in views:
        out += [o for o in observe(v) if by_view[o.deal_id].at(o.t) < cutoff]
    return out


def _resolve_volume(obs: list[Obs]) -> list[Obs]:
    """The volume control's option is 'more outbound emails than the median previous deal'."""
    values = [o.value for o in obs if o.point == "high_outbound_volume"]
    median = statistics.median(values) if values else 0.0
    return [o if o.took is not None else Obs(o.deal_id, o.point, o.t, o.value > median, o.value, o.won, o.close_t,
                                             o.reaction, o.reaction_t) for o in obs]


def _test(point: catalog.DecisionPoint, obs: list[Obs]) -> dict:
    taken, other = [o for o in obs if o.took], [o for o in obs if not o.took]
    w1, w0 = sum(o.won for o in taken), sum(o.won for o in other)
    testable = len(taken) >= MIN_PER_OPTION and len(other) >= MIN_PER_OPTION
    p = fisher_exact_p(w1, len(taken) - w1, w0, len(other) - w0) if testable else None
    return {"decision_point": point.id, "kind": point.kind, "n_option": len(taken), "n_other": len(other),
            "wins_option": w1, "wins_other": w0,
            "win_rate_option": round(w1 / len(taken), 6) if taken else None,
            "win_rate_other": round(w0 / len(other), 6) if other else None,
            "p_value": p, "promoted": False}


def _iso(t: datetime) -> str:
    return t.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _evidence(view: DealView, o: Obs, point: str, cutoff: datetime) -> list[dict]:
    ep = det_uuid("episode", o.deal_id, point)
    out = [{"kind": "decision_episode", "ref_id": ep, "at": _iso(view.at(o.t))},
           {"kind": "business_outcome", "ref_id": det_uuid("outcome", o.deal_id, point),
            "outcome_type": "closed_won" if o.won else "closed_lost", "at": _iso(view.at(o.close_t))}]
    if o.reaction is not None and o.reaction_t is not None and view.at(o.reaction_t) < cutoff:
        out.append({"kind": "customer_reaction", "ref_id": det_uuid("reaction", o.deal_id, point),
                    "polarity": o.reaction, "at": _iso(view.at(o.reaction_t))})
    return out


def _item(point: catalog.DecisionPoint, h: dict, obs: list[Obs], views: dict[str, DealView], cutoff: datetime) -> dict:
    better = h["win_rate_option"] > h["win_rate_other"]
    wording = point.better if better else point.worse
    recommended_taken = better  # the option is recommended when it wins more often, else its opposite is
    chosen = [o for o in obs if bool(o.took) == recommended_taken]
    evidence = sorted((e for o in chosen for e in _evidence(views[o.deal_id], o, point.id, cutoff)),
                      key=lambda e: (e["at"], e["kind"], e["ref_id"]))
    episodes = sorted({e["ref_id"] for e in evidence if e["kind"] == "decision_episode"})
    summary = (f"Learned from {h['n_option'] + h['n_other']} previous deals at this decision point: when the seller "
               f"{point.option}, {h['wins_option']} of {h['n_option']} closed won versus {h['wins_other']} of "
               f"{h['n_other']} otherwise (Fisher exact p = {h['p_value']:.4f}).")
    return {
        "id": det_uuid("knowledge", point.id), "key": point.key, "decision_point": point.id, "title": wording.title,
        "situation_signature": [dict(c) for c in point.signature],
        "guidance": {"summary": summary, "do": [wording.do], "dont": [wording.dont]},
        "created_at": _iso(cutoff), "recommended_option_taken": recommended_taken, "direction": "better" if better else "worse",
        "supporting_episode_ids": episodes, "source_decision_episode_id": episodes[0], "evidence": evidence,
    }


def learn(views: Sequence[DealView], cutoff: datetime) -> dict:
    """The frozen learning evidence (uplift_learning.v1) from the previous deals' views."""
    usable, late = _usable(views, cutoff)
    by_id = {v.deal_id: v for v in usable}
    obs = _resolve_volume(_observations(usable, cutoff))
    hyps = [_test(p, [o for o in obs if o.point == p.id]) for p in catalog.POINTS]
    for h, keep in zip(hyps, benjamini_hochberg([h["p_value"] for h in hyps], PROMOTION_Q)):
        h["promoted"] = keep
    knowledge = [_item(catalog.BY_ID[h["decision_point"]], h, [o for o in obs if o.point == h["decision_point"]], by_id, cutoff)
                 for h in hyps if h["promoted"]]
    episodes = sorted({det_uuid("episode", o.deal_id, o.point) for o in obs})
    return {"version": "uplift_learning.v1", "cutoff": _iso(cutoff), "previous_deals": len(usable),
            "excluded_closing_after_cutoff": late, "promotion_rule": PROMOTION_RULE, "q": PROMOTION_Q,
            "min_per_option": MIN_PER_OPTION, "hypotheses": hyps, "knowledge": knowledge, "previous_episode_ids": episodes}


def canonical_sha256(doc: object) -> str:
    return hashlib.sha256(json.dumps(doc, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")).hexdigest()
