"""The deterministic decision score: how the hidden rules rate the action the account agent chose.

Bench-only and never shown to the agent, the evals, the knowledge store or the learner: this is the one module that
links a decision point to the hidden rule that rates it (rules.v1.json, hidden-only, eval_known contributions zeroed).

A decision is the agent's PREFERRED candidate as generated (worker ranking 1, before core re-ranks it on eval
failures and before any revision), classified from the candidate's intent-level text (strategy type, title and
one-line description; never the body, which is full of negations such as 'we will not discount'). The classifier is a
lexicon with a negation rule: an option is realised when its phrases match and the opposite course's phrases do not.
An unclassifiable candidate is not a realisation (score 0).

  price_pushback   option: change the quoted amount (re-price, discount, restructure the offer smaller);
                   recommendation of the learned item: hold the amount and make the value case.
  second_quote     option: send a separate new quote;
                   recommendation of the learned item: fold the change into the open quote.
"""
from __future__ import annotations

import re
from collections.abc import Mapping
from typing import Any

from bench.synthetic import rules as R

RULE_OF: Mapping[str, str] = {
    "price_pushback": "syn1_reprice_after_quote_pushback",
    "second_quote": "syn1_second_quote_within_30d",
    "ops_outreach": "syn1_ops_contact_before_quote",
    "technical_reply": "syn1_cc_colleague_on_technical_reply",
}


def rule_value(point: str, rule_set: R.RuleSet | None = None) -> float:
    """Hidden-only planted log-odds of the option at this decision point (the rule's own, eval_known zeroed)."""
    rs = rule_set or R.load_rules()
    return next(r.log_odds for r in rs.rules if r.key == RULE_OF[point] and r.group == "hidden_company_specific")


def _re(*alternatives: str) -> re.Pattern[str]:
    return re.compile(r"\b(?:" + "|".join(alternatives) + r")", re.I)


_PRICE_NOUN = r"(?:the |our |its |their )?(?:quoted |current |proposed )?(?:price|prices|pricing|quote|amount|fee|fees|offer|proposal|rate)"
REPRICE = _re(r"re-?pric\w*", r"discount\w*", r"price[- ](?:cut|reduction|concession|adjustment|drop)\w*",
              rf"(?:reduc|lower|cut|trim|adjust|revis|renegotiat|sweeten)\w*\s+{_PRICE_NOUN}", r"concession\w*",
              r"phased\b", r"smaller (?:initial )?(?:scope|pilot|footprint)", r"reduced (?:scope|price|footprint)",
              r"restructur\w*", r"flexib\w*", r"special (?:pricing|offer|rate)", r"price[- ]match\w*")
HOLD = _re(rf"(?:hold|keep|maintain|defend|protect|stand by|retain|preserv)\w*\s+{_PRICE_NOUN}", r"value[- ](?:case|based|justification|story|proposition|selling|focus)",
           r"\broi\b", r"return on investment", r"business case", r"rather than (?:discount|cut|lower|reduc)\w*",
           r"without (?:discount|cut|lower|reduc|concession)\w*", r"no (?:discount|price cut|concession)\w*",
           r"justify (?:the )?(?:price|pricing|investment)", r"price integrity", r"defend\w* (?:the )?(?:price|pricing)",
           r"\bvalue (?:over|not) price")
SEPARATE = _re(r"(?:send|issue|prepare|create|provide|submit|deliver|draft|generate|share)\w*\s+(?:a |an |the )?(?:new|separate|second|additional|standalone|fresh|distinct)\s+(?:\w+\s+)?(?:quote|proposal)",
               r"separate (?:quote|proposal)", r"second (?:quote|proposal)", r"new (?:quote|proposal)", r"additional (?:quote|proposal)",
               r"standalone (?:quote|proposal)", r"parallel (?:quote|proposal)")
FOLD = _re(r"fold\w*", r"consolidat\w*", r"combin\w*", r"merg\w*", r"amend\w*", r"supersed\w*",
           r"updat\w*\s+(?:the |that |this )?(?:existing|open|earlier|current|original|prior|previous|outstanding)", r"(?:existing|open|earlier|outstanding|prior|previous) (?:quote|proposal)",
           r"single (?:quote|proposal)", r"one (?:quote|proposal)", r"same (?:quote|proposal)", r"revis\w+\s+(?:the |that )?(?:existing|open|earlier|outstanding)",
           r"rather than (?:a )?(?:new|separate|second)")

_OPTION = {"price_pushback": (REPRICE, HOLD), "second_quote": (SEPARATE, FOLD)}


def intent_text(candidate: Mapping[str, Any]) -> str:
    """The intent-level text of a candidate: strategy type, title and one-line description."""
    return " ".join([str(candidate.get("strategy_type", "")).replace("_", " "), str(candidate.get("title", "")),
                     str(candidate.get("description", ""))])


def _matches(pattern: re.Pattern[str], text: str) -> list[str]:
    return sorted({m.group(0).lower() for m in pattern.finditer(text)})


def classify(point: str, candidate: Mapping[str, Any]) -> dict[str, Any]:
    """{'option': bool, 'recommendation': bool, 'evidence': [matched phrases]} for the candidate at a decision point."""
    option_re, other_re = _OPTION[point]
    text = intent_text(candidate)
    opt, oth = _matches(option_re, text), _matches(other_re, text)
    return {"option": bool(opt) and not oth, "recommendation": bool(oth) and not opt,
            "evidence": [f"option:{p}" for p in opt] + [f"opposite:{p}" for p in oth]}


def decision_score(point: str, realized_option: bool, rule_set: R.RuleSet | None = None) -> tuple[float, float]:
    """(score, regret): the planted log-odds contribution of the chosen option, and best minus chosen."""
    beta = rule_value(point, rule_set)
    score = beta if realized_option else 0.0
    return score, _regret(beta, score)


def _regret(beta: float, score: float) -> float:
    best = max(beta, 0.0)  # the better of {option, not option}: not taking the option is worth 0
    return round(best - score, 12)
