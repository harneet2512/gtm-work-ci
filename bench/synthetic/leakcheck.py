"""Leak detection for planted-rule text and for the synthetic marker (spec §g).

A marker is a rule key, a whole rule statement, or any run of SHINGLE consecutive words of a
statement, after normalisation (lower case, every non-alphanumeric run collapsed to one space).
Matching is on word boundaries, so a reworded case, punctuation or a partial copy still hits.
Every file that decodes as UTF-8 text is scanned whatever its suffix (read once); JSON files are
also scanned as their decoded strings, so a statement wrapped across a \\n escape still hits.
An n-gram scan cannot catch paraphrase: the semantic side is the audit table in rules.v1.json
(hidden rules: nearest eval/knowledge and why it is not encoded) plus the key-term test.
"""
from __future__ import annotations

import json
import re
from collections.abc import Iterable, Iterator, Mapping, Sequence
from pathlib import Path
from typing import Any

from .rules import RuleSet

SHINGLE = 8
SKIP_DIRS = {"__pycache__", ".pytest_cache", "node_modules", ".git"}
ENVELOPE_MARKERS = ("origin", "provenance")
MARKER_TEXT = re.compile(r"syn1|synthetic", re.I)
_NON_ALNUM = re.compile(r"[^0-9a-z]+")


def normalize(text: str) -> str:
    return _NON_ALNUM.sub(" ", text.lower()).strip()


def markers(rule_set: RuleSet) -> set[str]:
    out: set[str] = set()
    for rule in rule_set.rules:
        out.add(normalize(rule.key))
        stmt = normalize(rule.statement)
        out.add(stmt)
        words = stmt.split()
        out.update(" ".join(words[i:i + SHINGLE]) for i in range(len(words) - SHINGLE + 1))
    return out


def _read_text(path: Path) -> str | None:
    """File content if it is UTF-8 text without NUL bytes, else None (binary)."""
    data = path.read_bytes()
    if b"\x00" in data:
        return None
    try:
        return data.decode("utf-8")
    except UnicodeDecodeError:
        return None


def text_files(roots: Iterable[Path]) -> Iterator[tuple[Path, str]]:
    """(path, text) for every text file below roots, each read exactly once."""
    for root in roots:
        candidates = [root] if root.is_file() else sorted(root.rglob("*"))
        for path in candidates:
            if path.is_file() and not SKIP_DIRS & set(path.parts):
                text = _read_text(path)
                if text is not None:
                    yield path, text


def strings(node: Any) -> Iterator[str]:
    if isinstance(node, str):
        yield node
    elif isinstance(node, dict):
        for k, v in node.items():
            yield k
            yield from strings(v)
    elif isinstance(node, list):
        for v in node:
            yield from strings(v)


def _views(path: Path, text: str, allow: Sequence[re.Pattern[str]] = ()) -> list[str]:
    """Normalised views of a file. Valid JSON / JSON lines: each decoded string on its own (escapes resolved, and
    no phrase across a field boundary such as 'EU operations' + 'person_id'); anything else: the raw text. Text
    matching an allow pattern (the observable activity text a feature is made of) is blanked."""
    views = [text]
    if path.suffix in {".json", ".jsonl"}:
        decoded: list[str] = []
        try:
            for chunk in (text.splitlines() if path.suffix == ".jsonl" else [text]):
                decoded.extend(strings(json.loads(chunk)) if chunk.strip() else [])
            views = decoded
        except ValueError:
            pass
    out = []
    for v in views:
        norm = f" {normalize(v)} "
        for pattern in allow:
            norm = pattern.sub(" ", norm)
        out.append(norm)
    return out


def scan(roots: Iterable[Path], needles: set[str], allow: Sequence[str] = (),
         snippets: Mapping[Path, Sequence[str]] | None = None) -> list[tuple[Path, str]]:
    """(file, marker) for every marker found in a text file below roots. allow = regexes on normalised text
    (anywhere); snippets = exact normalised text blanked ONLY in the given file (term_allowlist.v1.json)."""
    patterns = [re.compile(a) for a in allow]
    per_file = {p.resolve(): [f" {s} " for s in v] for p, v in (snippets or {}).items()}
    hits: list[tuple[Path, str]] = []
    for path, text in text_files(roots):
        views = _views(path, text, patterns)
        for snip in per_file.get(path.resolve(), ()):
            views = [v.replace(snip, " ") for v in views]
        hits.extend((path, m) for m in sorted(needles) if any(f" {m} " in v for v in views))
    return hits


def marker_leaks(event: Mapping[str, Any]) -> list[str]:
    """Agent-visible strings of a SourceEvent that reveal it is synthetic (everything but the envelope marker)."""
    visible = {k: v for k, v in event.items() if k not in ENVELOPE_MARKERS}
    return [s for s in strings(visible) if MARKER_TEXT.search(s)]


def scan_generated(events_dir: Path, rule_set: RuleSet) -> list[str]:
    """Generation-time QA (stage qa_gates): rule text or a visible marker in any generated event fails the run."""
    problems = [f"{p}: rule text {m!r}" for p, m in scan([events_dir], markers(rule_set))]
    for path, text in text_files([events_dir]):
        if path.suffix == ".json":
            doc = json.loads(text)
            for event in doc if isinstance(doc, list) else [doc]:  # a generated file holds one deal's events
                problems += [f"{path}: visible marker {s!r}" for s in marker_leaks(event)]
    return problems
