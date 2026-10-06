"""Reference evaluator for the transition rule set (ADR-0012): the executable spec the Go transition detector must match.

Pure: given one AccountState (with relationship_state), the account's signals and claims, the evaluation time (the
version's as_of), its computed_at and the account's open transition (if any), it returns the status and facts the rule
set's evaluation steps (rules.v1.json#/evaluation) imply. Signals carry `open` (ADR-0011 openness is computed upstream)
and may carry subject_person_id; claims carry field_path, standing, occurred_at, status, subject_person_id and
source_activity_id."""
from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone

OPEN_ITEM_STATUSES = (None, "open", "overdue")


def ts(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


@dataclass(frozen=True)
class Context:
    state: dict
    signals: tuple = ()
    claims: tuple = ()
    now: str = ""  # evaluation time = the AccountState version's as_of
    open_transition: dict | None = None  # {"status", "to_state_candidate", "last_updated_at"}
    computed_at: str | None = None  # the version computed_at; informational: confirmed_at is the as_of (now)
    deals: tuple = ()  # per-deal states ({opportunity_id, is_open, fields, buying_group}); empty: the account headline is the only scope


@dataclass(frozen=True)
class FactResult:
    key: str
    description: str
    required: bool
    satisfied: bool
    evidence_refs: tuple = ()
    signal_ids: tuple = ()
    rejects: bool | None = None

    def as_contract(self) -> dict:
        doc = {"key": self.key, "description": self.description, "required": self.required, "satisfied": self.satisfied,
               "evidence_refs": list(self.evidence_refs), "signal_ids": list(dict.fromkeys(self.signal_ids))}
        if self.rejects is not None:
            doc["rejects"] = self.rejects
        return doc


@dataclass(frozen=True)
class Outcome:
    status: str | None  # None: no transition; CONFIRMED / CANDIDATE / UNRESOLVED / REJECTED
    to_state: str | None = None
    rule_id: str | None = None
    supporting: tuple = ()
    missing: tuple = ()
    contradicting: tuple = ()
    confidence: float = 0.0
    closed: bool = False
    confirmed_at: str | None = None
    facts: dict = field(default_factory=dict)


class _Evaluator:
    def __init__(self, rules: dict, ctx: Context) -> None:
        self.rules, self.ctx, self.now = rules, ctx, ts(ctx.now)
        relationship = ctx.state.get("relationship_state", {})
        confirmed_at = relationship.get("confirmed_at")
        # no confirmed state ('unknown') means no anchor and no lower bound: every claim and signal is new
        unconfirmed = not confirmed_at and relationship.get("value", "unknown") == "unknown"
        self.anchor = ts(confirmed_at) if confirmed_at else (datetime.min.replace(tzinfo=timezone.utc) if unconfirmed else None)
        self.champion = ctx.state["fields"].get("champion", {}).get("value")

    def days(self, name: str) -> timedelta:
        return timedelta(days=self.rules["thresholds"][name]["value"])

    def _in_window(self, at: str, cond: dict) -> bool:
        moment = ts(at)
        if moment > self.now:
            return False
        if cond["op"] in ("fired_within_days", "asserted_within_days") and self.now - moment > self.days(cond["threshold"]):
            return False
        return "since" not in cond or (self.anchor is not None and moment > self.anchor)  # strictly after

    def _about(self, cond: dict, subject: str | None) -> bool:
        about = cond.get("about")
        if about is None:
            return True
        is_champion = subject is not None and subject == self.champion
        return is_champion if about == "champion" else not is_champion

    def _compare(self, cond: dict, value: object) -> bool:
        op, want = cond["op"], cond.get("value")
        return value == want if op == "eq" else value != want if op == "neq" else value in (want or [])

    def _state(self, cond: dict, name: str, earning: bool) -> tuple[bool, list, list]:
        f = self.ctx.state["fields"].get(name) or {"known": False, "value": "unknown", "evidence_refs": []}
        refs, op = list(f.get("evidence_refs", [])), cond["op"]
        if cond["path"].endswith(".standing"):
            return f.get("known", False) and self._compare(cond, f.get("standing")), refs, []
        if earning and f.get("known") and f.get("standing") is not None and f["standing"] not in self.rules["claim_standings"]:
            return False, [], []  # a field won by third-party enrichment never earns a state
        if op == "is_unknown":
            return not f["known"], [], []
        if not f["known"]:
            return False, [], []
        if op == "has_item":
            items = [i for i in f["value"] if i.get("kind") == cond["value"] and i.get("status") in OPEN_ITEM_STATUSES
                     and ("since" not in cond or any(self._in_window(r["occurred_at"], cond) for r in i.get("evidence_refs", [])))]
            return bool(items), [r for i in items for r in i.get("evidence_refs", [])], []
        if op == "age_days_gte":
            return self.now - ts(f["value"]) >= self.days(cond["threshold"]), refs, []
        if ("since" in cond or op == "asserted_within_days") and not (f.get("as_of") and self._in_window(f["as_of"], cond)):
            return False, [], []
        if op in ("known", "asserted_within_days"):
            return True, refs, []
        return self._compare(cond, f["value"]), refs, []

    def _diff(self, cond: dict, name: str, earning: bool) -> tuple[bool, list, list]:
        sigs = [s for s in self.ctx.signals if s["signal_type"] == name]
        at = lambda s: s.get("occurred_at") or s["created_at"]  # noqa: E731  occurred_at is when it happened, created_at when we recorded it
        if cond["op"] == "not_exists":
            return not any(s["open"] for s in sigs), [], []
        # a signal earns a fact only when first-party (`first_party` absent means assessed and eligible)
        hits = [s for s in sigs if (s["open"] and ("since" not in cond or self._in_window(at(s), cond))
                                    if cond["op"] == "exists" else self._in_window(at(s), cond))
                and self._about(cond, s.get("subject_person_id")) and (not earning or s.get("first_party", True))]
        return bool(hits), [r for s in hits for r in s.get("evidence_refs", [])], [s["id"] for s in hits]

    def _claim(self, cond: dict, name: str) -> tuple[bool, list, list]:
        standings = self.rules["claim_standings"]
        stance = cond.get("stance", "stated")  # a withdrawn claim ("the rollout is off") is evidence of the opposite, never of the need
        hits = [c for c in self.ctx.claims if c["field_path"] == name and c["status"] in self.rules["claim_statuses"]
                and ("withdrawn" if c.get("withdrawn") else "stated") == stance
                and c.get("standing", "first_party_ai") in standings and self._in_window(c["occurred_at"], cond)
                and self._about(cond, c.get("subject_person_id"))]
        return bool(hits), [{"activity_id": c["source_activity_id"], "claim_id": c["id"]} for c in hits], []

    def _members(self, cond: dict, role: str) -> tuple[bool, list, list]:
        statuses, where = self.rules["buying_group_member_statuses"], cond.get("where", {})
        # a tenure test looks at every member holding the role except a departed one: an acting owner who never wrote still counts
        usable = (lambda m: m["status"] != "departed") if "tenure" in where else (lambda m: m["status"] in statuses)
        hits = [m for m in self.ctx.state["buying_group"] if role in m["roles"] and usable(m)
                and all(m.get(k, "unknown") == v for k, v in where.items())]
        if cond["op"] == "not_exists":
            return not hits, [], []
        return bool(hits), [r for m in hits for r in m.get("evidence_refs", [])], []

    def condition(self, cond: dict, earning: bool) -> tuple[bool, list, list]:
        kind, _, rest = cond["path"].partition(".")
        if kind == "state":
            return self._state(cond, rest.split(".")[0], earning)
        if kind == "diff":
            return self._diff(cond, rest, earning)
        handler = {"claim": self._claim, "buying_group": self._members}[kind]
        return handler(cond, rest)

    def fact(self, fact: dict, earning: bool = True) -> FactResult:
        """earning: a fact the transition rests on; contradictions are evaluated with earning=False (every signal counts)."""
        for group in fact["any_of"]:
            results = [self.condition(c, earning) for c in group["all_of"]]
            if all(held for held, _, _ in results):
                refs = [r for _, rs, _ in results for r in rs]
                sigs = [s for _, _, ss in results for s in ss]
                unique = tuple(dict((repr(sorted(r.items())), r) for r in refs).values())
                if not unique:
                    continue  # a group that cites no evidence does not satisfy the fact
                return FactResult(fact["key"], fact["description"], fact["required"], True, unique, tuple(sigs), fact.get("rejects"))
        return FactResult(fact["key"], fact["description"], fact["required"], False, (), (), fact.get("rejects"))


def _scopes(rules: dict, rule: dict, ctx: Context) -> list[_Evaluator]:
    """One evaluator per scope the rule reads. With per-deal states the deal-scoped facts are read from every open
    deal ("any open deal satisfies"), never from the account headline; no open deal falls back to the headline."""
    if not ctx.deals:
        return [_Evaluator(rules, ctx)]
    open_deals = [d for d in ctx.deals if d["is_open"]] or [None]
    out = []
    for d in open_deals:
        state = ctx.state if d is None else {**ctx.state, "fields": d["fields"], "buying_group": d["buying_group"]}
        keep = (lambda x: True) if d is None or rule.get("claim_scope", "deal") == "account" else \
            (lambda x, d=d: x.get("opportunity_id") in (None, d["opportunity_id"]))
        out.append(_Evaluator(rules, Context(state, tuple(s for s in ctx.signals if keep(s)), tuple(c for c in ctx.claims if keep(c)),
                                             ctx.now, ctx.open_transition, ctx.computed_at)))
    return out


def _rule_outcome(rule: dict, evs: list[_Evaluator], previous: dict) -> dict:
    ev0 = evs[0]
    facts = {}
    for f in rule["facts"]:
        results = [ev.fact(f) for ev in evs]
        facts[f["key"]] = next((r for r in results if r.satisfied), results[0])
    contradicting: dict = {}
    for ev in evs:
        for c in rule["contradictions"]:
            r = ev.fact(c, earning=False)
            if r.satisfied and (c["key"] not in contradicting):
                contradicting[c["key"]] = r
    contradicting = tuple(contradicting[c["key"]] for c in rule["contradictions"] if c["key"] in contradicting)
    held = {k for k, r in facts.items() if r.satisfied}
    gate = rule["candidate"]
    need = ev0.rules["thresholds"][gate["min_satisfied"]["threshold"]]["value"]
    required = rule["confirmed"]["requires_all"]
    decisive = any(c.rejects for c in contradicting)
    # a rule may demand a recorded CANDIDATE for its own target before it confirms (REORG: never from a single evaluation)
    allowed = (not rule["confirmed"].get("after_candidate")
               or (previous.get("status") == "CANDIDATE" and previous.get("to_state_candidate") == rule["to_state"]))
    return {
        "rule": rule, "facts": facts, "contradicting": contradicting, "decisive": decisive,
        "confirms": set(required) <= held and not decisive and allowed,
        "pending_candidate": set(required) <= held and not decisive and not allowed,
        "gated": bool(held & set(gate["requires_any"])) and len(held & set(gate["min_satisfied"]["of"])) >= need and not decisive,
        "confidence": round(sum(1 for k in required if k in held) / len(required), 3),
    }


PENDING = FactResult("candidate_recorded", "The evidence is complete; the transition is confirmed on the evaluation after this candidate is recorded.",
                     True, False)


def _single(status: str, o: dict, confirmed_at: str | None = None) -> Outcome:
    facts = o["facts"].values()
    missing = tuple(f for f in facts if not f.satisfied)
    if status == "CANDIDATE" and o["pending_candidate"]:
        missing += (PENDING,)  # a CANDIDATE always has an unmet required fact; here it is the recorded candidate itself
    return Outcome(status, o["rule"]["to_state"], o["rule"]["id"], tuple(f for f in facts if f.satisfied),
                   missing, o["contradicting"], o["confidence"],
                   confirmed_at=confirmed_at, facts=o["facts"])


def _unresolved(outcomes: list[dict], closed: bool = False) -> Outcome:
    """UNRESOLVED keeps the facts of the rule(s) it came from (first occurrence of a key wins)."""
    facts: dict = {}
    contradicting: dict = {}
    for o in outcomes:
        for key, f in o["facts"].items():
            facts.setdefault(key, f)
        for c in o["contradicting"]:
            contradicting.setdefault(c.key, FactResult(c.key, c.description, False, True, c.evidence_refs, c.signal_ids, False))
    values = facts.values()
    return Outcome("UNRESOLVED", None, None, tuple(f for f in values if f.satisfied), tuple(f for f in values if not f.satisfied),
                   tuple(contradicting.values()), max((o["confidence"] for o in outcomes), default=0.0), closed, facts=facts)


def _choose(status: str, picked: list[dict], target: str | None, involved: list[dict], ctx: Context) -> Outcome | None:
    """Exactly one rule with the open target (or none open) -> status; two or more, or a different target -> UNRESOLVED."""
    if not picked:
        return None
    if len(picked) == 1 and target in (None, picked[0]["rule"]["to_state"]):
        return _single(status, picked[0], ctx.now if status == "CONFIRMED" else None)
    return _unresolved(picked + [o for o in involved if o not in picked])


def evaluate(rules: dict, ctx: Context) -> Outcome:
    """Apply the rule set's evaluation steps in order."""
    ev = _Evaluator(rules, ctx)  # account scope: the clock, the anchor and the stale window
    current = ctx.state.get("relationship_state", {}).get("value", "unknown")
    previous = ctx.open_transition or {}
    outcomes = [_rule_outcome(r, _scopes(rules, r, ctx), previous) for r in rules["transitions"] if current in r["from_states"]]
    target = previous.get("to_state_candidate")
    own = [o for o in outcomes if target is not None and o["rule"]["to_state"] == target]
    if previous.get("status") == "CANDIDATE" and own and own[0]["decisive"]:  # step 2
        return _single("REJECTED", own[0])
    for status, key in (("CONFIRMED", "confirms"), ("CANDIDATE", "gated")):  # steps 3-4
        chosen = _choose(status, [o for o in outcomes if o[key]], target, own, ctx)
        if chosen is not None:
            return chosen
    if previous.get("status") == "CANDIDATE":  # step 5: lost gate
        return _unresolved(own)
    if previous.get("status") == "UNRESOLVED":  # step 6: stale close
        return _unresolved(outcomes, closed=ev.now - ts(previous["last_updated_at"]) >= ev.days("unresolved_stale_days"))
    return Outcome(None)
