"""Verbatim evidence-quote verification.

Order: (1) exact substring; (2) match after Unicode NFC + whitespace collapsing, mapped back to the exact slice
of the original text; a collapsed match may not span a blank line. Anything else is not verbatim.
"""
from __future__ import annotations

import re
import unicodedata

from ..models.claim_candidate import MAX_EVIDENCE_QUOTE_CHARS

_BLANK_LINE = re.compile(r"\n\s*\n")


def _units(text: str) -> list[tuple[int, int]]:
    """Split into (start, end) units: one whitespace char, or a base char plus its combining marks."""
    units: list[tuple[int, int]] = []
    i, n = 0, len(text)
    while i < n:
        j = i + 1
        if not text[i].isspace():
            while j < n and unicodedata.combining(text[j]):
                j += 1
        units.append((i, j))
        i = j
    return units


class QuoteMatcher:
    """Whitespace-collapsed, NFC view of `text` with offsets back into the original (built once per text)."""

    def __init__(self, text: str) -> None:
        self._text = text
        chars: list[str] = []
        spans: list[tuple[int, int]] = []
        blank: list[bool] = []
        run_start = -1
        for start, end in _units(text):
            if text[start].isspace():
                if run_start < 0:
                    run_start = start
                run_end = end
                continue
            if run_start >= 0:
                self._push_space(chars, spans, blank, run_start, run_end)
                run_start = -1
            for char in unicodedata.normalize("NFC", text[start:end]):
                chars.append(char)
                spans.append((start, end))
                blank.append(False)
        if run_start >= 0:
            self._push_space(chars, spans, blank, run_start, run_end)
        self._norm = "".join(chars)
        self._spans = spans
        self._blank = blank

    def _push_space(self, chars: list[str], spans: list[tuple[int, int]], blank: list[bool],
                    start: int, end: int) -> None:
        chars.append(" ")
        spans.append((start, end))
        blank.append(bool(_BLANK_LINE.search(self._text[start:end])))

    def resolve(self, raw: object) -> str | None:
        """Return the exact substring of the text that `raw` quotes, or None when it is not verbatim."""
        if not isinstance(raw, str) or not raw.strip():
            return None
        trimmed = raw.strip()
        quote = trimmed if trimmed in self._text else self._find_normalized(trimmed)
        if quote is None or len(quote) > MAX_EVIDENCE_QUOTE_CHARS:
            return None
        return quote

    def _find_normalized(self, quote: str) -> str | None:
        needle = " ".join(unicodedata.normalize("NFC", quote).split())
        if not needle:
            return None
        start = self._norm.find(needle)
        while start >= 0:
            end = start + len(needle)  # exclusive, in normalized coordinates
            if not any(self._blank[start:end]):
                return self._text[self._spans[start][0]:self._spans[end - 1][1]]
            start = self._norm.find(needle, start + 1)
        return None
