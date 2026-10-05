"""Meaning check of a paraphrased email (WP32 realism pass; Opus review of PR #40, HIGH 2).

`paraphrase_check.check` only looks at surface tokens. This module compares what the paraphrase MEANS with what
its template means, with deterministic rules (no model call):

  - intent: the paraphrase is re-classified (reply kind and polarity, from cue classes such as go-ahead, pause,
    price concern, leaving, handover) and must match the template's kind, polarity and full cue signature;
  - negation: the number of negation cues must be equal (a dropped or added "not" flips a claim);
  - roles: role phrases (economic buyer, main contact, ...) may be neither added nor removed;
  - time and quantity: every number, number word, month, weekday, duration unit, relative date ("next week",
    "tomorrow") and quantity word of the template must survive, and none may be added.

Slot values are masked in both texts first, so a name or product that happens to contain a cue word is neutral.
The check is deliberately strict: a false reject only costs a fallback to the template text.
"""
from __future__ import annotations

import re
from collections.abc import Mapping

CUES: dict[str, re.Pattern[str]] = {k: re.compile(v) for k, v in {
    "go": r"\b(go(ing)? ahead|mov(e|ing) (forward|ahead)|proceed(ing)?|sign(ing)?|approv(e|al|ed)|green ?light|commit(ting)? to)\b",
    "stop": r"\b(paus(e|ed|ing)|hold(ing)? off|on hold|postpone[sd]?|delay(ed|ing)?|wait(ing)?|revisit|pick (this|it) up again|"
            r"not (yet|ready)|until (after )?(\x00|that)|come back to (it|this))\b",
    "price": r"\b(pric(e|es|ing)|cost(s)?|budget|expensive|discount|afford(able)?|flexibility)\b",
    "security": r"\b(security|compliance|certifications?|data handling|access controls?)\b",
    "integration": r"\b(integrat(e|ion)|duplicated|existing work|tools we already|transition)\b",
    "leave": r"\b(leav(e|ing)|depart(ing|ure)?|resign(ing)?|new role elsewhere|moving on)\b",
    "move_away": r"\b(different area|no longer|new arrangement|moving into)\b",
    "reorg": r"\b(restructur(e|ing)|reorgani[sz](e|ation|ing)|being combined|merged|reassign(ed|ment)|new structure|organi[sz]ational)\b",
    "handover": r"\b(take (the )?lead|take(s)? over|hand(ing)? over|handover|good hands|decisions on|full support|work with .{0,30} directly)\b",
    "intro": r"\b(joined|introduc(e|ing) (myself|you)|(become|became|being|are) involved|recently become|helping the team assess|"
             r"bring you up to speed|new to)\b",
    "expand": r"\b(another group|other team|broader|extend(ing)?|business units?|their work|rollout to another)\b",
    "value": r"\b(reduction|saves?|saved|time saved|the return|return on|roi|outcomes?|results?|benefits?)\b",
    "positive": r"\b(encouraging|positive|glad|interest(ed)?|welcome|useful|appreciat(e|ed)|confident|well)\b",
    "negative": r"\b(concern(s|ed)?|difficult|hard to|higher than|candid|worried|problem|hold things up)\b",
}.items()}
NEGATION = re.compile(r"\b(not|no|never|cannot|without|neither|nor|nothing|none|unable|nobody)\b|n['’]t\b", re.IGNORECASE)
ROLES = re.compile(r"\b(champion|sponsor|decision[- ]?maker|economic buyer|technical evaluator|evaluator|end user|owner(ship)?|"
                   r"point of contact|main contact|primary contact|successor|replacement|procurement|finance|operations|"
                   r"security team|legal|engineering|business units?|lead)\b", re.IGNORECASE)
UNITS = {"day", "week", "month", "quarter", "year", "hour", "minute"}
MONTHS = ("january february march april june july august september october november december jan feb apr jun jul aug sept oct "
          "nov dec monday tuesday wednesday thursday friday saturday sunday").split()
NUMBER_WORDS = ("zero one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen twenty thirty "
                "forty fifty sixty hundred thousand million first second third fourth fifth half dozen").split()
QUANTITY = ("couple few several both all every each").split()
SOME = {"couple", "few", "several"}  # interchangeable quantity words
ROLE_SYNONYMS = {"point of contact": "contact", "main contact": "contact", "primary contact": "contact",
                 "decision maker": "decision-maker", "decisionmaker": "decision-maker"}
RELATIVE = re.compile(r"\b(next|this|last|coming|following|previous|early|late|end of( the)?)\s+(week|month|quarter|year|day|"
                      r"weeks|months|days|quarters|years)\b")
WORDS = re.compile(r"[a-z]+")
NUMBER = re.compile(r"\d[\d,.:/%\-]*")
MAY = re.compile(r"\b(?:in|by|of|until|before|after|from|since|during|through|to|for|on|late|early|mid) May\b")
SLOT_MASK = "\x00"


def _mask(text: str, slots: Mapping[str, str]) -> str:
    for value in sorted({str(v) for v in slots.values() if v}, key=len, reverse=True):
        text = text.replace(value, SLOT_MASK)
    return text


def _stem(word: str) -> str:
    return word[:-1] if word.endswith("s") and word[:-1] in UNITS else word


def _time_tokens(text: str) -> set[str]:
    low = text.lower()
    out = {f"rel:{' '.join(m.group(0).split())}" for m in RELATIVE.finditer(low)}
    out |= {f"num:{n.strip('.,:-')}" for n in NUMBER.findall(low)}
    for w in WORDS.findall(low):
        w = _stem(w)
        if w in UNITS or w in MONTHS or w in NUMBER_WORDS or w in QUANTITY or w in {
                "today", "tomorrow", "yesterday", "tonight", "now", "immediately", "soon", "asap", "later", "earlier"}:
            out.add("w:some" if w in SOME else f"w:{w}")
    if MAY.search(text):
        out.add("w:may")
    return out


def signature(text: str) -> frozenset[str]:
    low = text.lower()
    return frozenset(name for name, pat in CUES.items() if pat.search(low))


def classify(text: str) -> tuple[str, str]:
    """(reply kind, polarity) of a text by cue classes: the deterministic re-classification of a paraphrase."""
    s = signature(text)
    if s & {"price", "security", "integration", "negative"}:
        return "objection", "negative"
    if "stop" in s and "go" not in s:
        return "timing_request", "defer"
    if "leave" in s:
        return "champion_leaves", "neutral"
    if "move_away" in s:
        return "champion_moves", "neutral"
    if "reorg" in s:
        return "reorg_announced", "neutral"
    if "handover" in s:
        return "written_handover", "neutral"
    if "intro" in s:
        return "stakeholder_intro", "positive"
    if "expand" in s:
        return "positive_need", "positive"
    if "value" in s and "positive" in s:
        return "positive_value", "positive"
    return "positive", "positive" if s & {"positive", "go"} else "neutral"


def meaning_reasons(paraphrase: str, template_text: str, slots: Mapping[str, str]) -> list[str]:
    """Reasons the paraphrase does not mean what its template means; empty when it does."""
    new, old = _mask(paraphrase, slots), _mask(template_text, slots)
    reasons: list[str] = []
    kind_new, kind_old = classify(new), classify(old)
    if kind_new != kind_old:
        reasons.append(f"intent changed: {kind_old[0]}/{kind_old[1]} became {kind_new[0]}/{kind_new[1]}")
    sig_new, sig_old = signature(new), signature(old)
    if sig_old - sig_new:
        reasons.append(f"intent cues removed {sorted(sig_old - sig_new)}")
    if sig_new - sig_old:
        reasons.append(f"intent cues added {sorted(sig_new - sig_old)}")
    n_new, n_old = len(NEGATION.findall(new)), len(NEGATION.findall(old))
    if n_new != n_old:
        reasons.append(f"negation changed ({n_old} cues became {n_new})")
    role_new = {ROLE_SYNONYMS.get(m.group(0).lower(), m.group(0).lower()) for m in ROLES.finditer(new)}
    role_old = {ROLE_SYNONYMS.get(m.group(0).lower(), m.group(0).lower()) for m in ROLES.finditer(old)}
    if role_new != role_old:
        reasons.append(f"role phrases changed: removed {sorted(role_old - role_new)}, added {sorted(role_new - role_old)}")
    t_new, t_old = _time_tokens(new), _time_tokens(old)
    if t_old - t_new:
        reasons.append(f"removed numbers or time words {sorted(t_old - t_new)}")
    if t_new - t_old:
        reasons.append(f"added numbers or time words {sorted(t_new - t_old)}")
    return reasons
