"""Helpers shared by the spec modules (HAR-114 gold v2). Specs are plain dicts; these build them without mutation."""
from __future__ import annotations

from typing import Any


def merge(base: dict[str, Any], **over: Any) -> dict[str, Any]:
    """A new dict: base with `over` applied one level deep for dict values."""
    out = dict(base)
    for key, value in over.items():
        out[key] = {**base.get(key, {}), **value} if isinstance(value, dict) and isinstance(base.get(key), dict) else value
    return out


def items(*texts: str, status: str = "open") -> list[dict[str, str]]:
    """List-valued state field items."""
    return [{"text": t, "status": status} for t in texts]


def spec(**kw: Any) -> dict[str, Any]:
    required = ("id", "type", "title", "deal", "trigger", "state", "recent", "cand", "best", "evals", "intent", "difficulty")
    missing = [k for k in required if k not in kw]
    if missing:
        raise ValueError(f"{kw.get('id', '?')}: spec lacks {missing}")
    return kw
