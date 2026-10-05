"""Validator of a paraphrased email body (WP32 realism pass).

(Meaning, not just tokens, is checked in paraphrase_meaning.py.) A paraphrase must keep every slot value that the template body contains and must not add a number, number
word, month, weekday, email address, link or person name that the template (with its slot values) does not
have. A failing paraphrase is rejected and the generator falls back to the template text.
"""
from __future__ import annotations

import re
from collections.abc import Collection, Mapping

from .paraphrase_meaning import meaning_reasons

WORD = re.compile(r"[A-Za-z][A-Za-z'’\-]*")
CONTRACTION = re.compile(r"['’](ll|m|ve|d|re|s|t)$", re.IGNORECASE)
NUMBER = re.compile(r"\d[\d,.:/%\-]*")
EMAIL = re.compile(r"\S+@\S+")
LINK = re.compile(r"https?://|www\.", re.IGNORECASE)
NUMBER_WORDS = frozenset("""zero one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen
twenty thirty forty fifty sixty hundred thousand million billion first second third fourth fifth half dozen""".split())
MONTHS = frozenset("""january february march april june july august september october november december jan feb apr jun
jul aug sept oct nov dec monday tuesday wednesday thursday friday saturday sunday""".split())  # not may/mar/sat/sun/mon
# Capitalised function and business words that start a sentence and are not names. Anything else capitalised
# that neither the template nor the real-email vocabulary knows is treated as a possible person or company name.
COMMON = frozenset("""a about after again all also am an and any are as at be because been before best both but by can
could dear did do does each either every few for from further get give go good great had has have he her here hi hello his
how i if in into is it its just kind let like look may me more most much my need new no not now of on one only or other our
please regards so some such sure thank thanks that the their them then there these they this those to too under up us very
was we well were what when where which who why will with would yes you your sincerely cheers hope looking glad happy
appreciate understood noted apologies unfortunately currently soon quick note update following""".split())


def _words(text: str) -> set[str]:
    return {w.lower() for w in WORD.findall(text)}


def _tokens(pattern: re.Pattern[str], text: str) -> set[str]:
    return {t.strip(".,:-") for t in pattern.findall(text)}


def check(paraphrase: str, template_text: str, slots: Mapping[str, str], vocab: Collection[str] = ()) -> list[str]:
    """Reasons the paraphrase is rejected; empty when it is acceptable. `template_text` is the template body with
    the slot values filled in; `slots` maps slot name to the value used."""
    reasons: list[str] = []
    text = paraphrase.strip()
    if not text:
        return ["empty"]
    for name, value in slots.items():
        value = str(value)
        if value and value in template_text and value not in text:
            reasons.append(f"slot {name} value {value!r} dropped or altered")
    if EMAIL.search(text) and not EMAIL.search(template_text):
        reasons.append("added an email address")
    if LINK.search(text) and not LINK.search(template_text):
        reasons.append("added a link")
    added = _tokens(NUMBER, text) - _tokens(NUMBER, template_text)
    if added:
        reasons.append(f"added numbers {sorted(added)}")
    known = _words(template_text) | {w.lower() for v in slots.values() for w in WORD.findall(str(v))}
    for label, group in (("number words", NUMBER_WORDS), ("dates", MONTHS)):
        extra = (_words(text) & group) - known
        if extra:
            reasons.append(f"added {label} {sorted(extra)}")
    lower_seen = {w for w in WORD.findall(text) if w[0].islower()}
    allowed = known | COMMON | {w.lower() for w in vocab} | {w.lower() for w in lower_seen}
    names = sorted({w for w in WORD.findall(text)
                    if w[0].isupper() and CONTRACTION.sub("", w.replace("n't", "")).lower() not in allowed})
    if names:
        reasons.append(f"added names {names}")
    return reasons + meaning_reasons(text, template_text, slots)
