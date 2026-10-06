"""Pure helpers for the CRMArena extraction report (HAR-104 / HAR-130): no database, no network.

Everything here is deterministic and unit-tested (worker-py/tests/test_bench_crmarena_extraction.py).
The CLI in crmarena_extraction_report.py feeds these functions with rows from a local Postgres, the
worker's structured log and the exported (git-ignored) CRMArena snapshot.
"""
from __future__ import annotations

import ast
import json
import math
import re
from collections import Counter
from collections.abc import Iterable, Sequence
from typing import Any

# Claim fields whose subject is a person: an unresolved subject on one of these is an
# entity-resolution miss (the claim is stored without subject_person_id).
PERSON_FIELDS = ("champion", "champion_status", "economic_buyer", "buying_group.member", "stakeholder_role", "delegation")

_AMOUNT = re.compile(r"\$\s?(\d[\d,]*(?:\.\d+)?)\s?([kKmM]|million|thousand)?\b")
_WORD = re.compile(r"[a-z0-9]+")
_STOP = frozenset({"the", "of", "and", "for", "a", "an", "to", "in", "at", "on", "senior", "sr", "jr", "junior", "lead"})
_STAGE_CLEAN = re.compile(r"[^a-z]+")


def percentile(values: Sequence[float], q: float) -> float | None:
    """Linear-interpolated percentile (q in 0..100); None for an empty sample."""
    if not values:
        return None
    ordered = sorted(values)
    pos = (len(ordered) - 1) * q / 100
    low, high = math.floor(pos), math.ceil(pos)
    return ordered[low] + (ordered[high] - ordered[low]) * (pos - low)


def rate(numerator: int, denominator: int) -> float | None:
    return None if denominator == 0 else round(numerator / denominator, 4)


def summarize(values: Sequence[float]) -> dict[str, float | int | None]:
    """count, mean, p50, p95, max of a numeric sample."""
    return {"count": len(values), "mean": round(sum(values) / len(values), 3) if values else None,
            "p50": percentile(values, 50), "p95": percentile(values, 95), "max": max(values) if values else None}


def parse_amounts(text: str) -> list[float]:
    """Dollar amounts in free text: '$1,200,000', '$1.2M', '$50K', '$2 million'."""
    out: list[float] = []
    for number, suffix in _AMOUNT.findall(text):
        value = float(number.replace(",", ""))
        factor = {"k": 1e3, "thousand": 1e3, "m": 1e6, "million": 1e6}.get(suffix.lower(), 1.0)
        out.append(value * factor)
    return out


def amount_agrees(claimed: Iterable[float], truth: float, tolerance: float = 0.01) -> bool:
    """True when any stated amount is within `tolerance` (relative) of the CRM amount."""
    return any(abs(c - truth) <= tolerance * abs(truth) for c in claimed) if truth else False


def content_tokens(text: str) -> set[str]:
    return {t for t in _WORD.findall(text.lower()) if t not in _STOP and len(t) > 1}


def title_overlap(claim_text: str, crm_title: str) -> bool:
    """A claimed role/title matches the CRM title when they share a content word (loose, by design:
    the extractor states roles in its own words, the CRM uses job titles)."""
    return bool(content_tokens(claim_text) & content_tokens(crm_title))


def normalize_stage(stage: str) -> str:
    return _STAGE_CLEAN.sub("", stage.lower())


def stage_agrees(claimed: str, truth: str) -> bool:
    """Exact after normalization, or one contains the other ('Closed Won' vs 'closed won - signed')."""
    a, b = normalize_stage(claimed), normalize_stage(truth)
    return bool(a) and bool(b) and (a == b or a in b or b in a)


def names_agree(claimed: str, truth: str) -> bool:
    """Same person: every token of the shorter name appears in the longer one."""
    a, b = _WORD.findall(claimed.lower()), _WORD.findall(truth.lower())
    if not a or not b:
        return False
    short, long_ = (a, b) if len(a) <= len(b) else (b, a)
    return all(t in long_ for t in short)


def _usage(raw: Any) -> dict[str, Any]:
    """The worker logs usage as a dict repr (its JSON formatter stringifies mappings); accept both."""
    if isinstance(raw, dict):
        return raw
    if isinstance(raw, str):
        try:
            parsed = ast.literal_eval(raw)
        except (ValueError, SyntaxError):
            return {}
        return parsed if isinstance(parsed, dict) else {}
    return {}


def parse_worker_log(lines: Iterable[str]) -> dict[str, Any]:
    """Aggregate the worker's JSON log: per-call latency and token usage, provider errors, hung retries."""
    latencies: list[float] = []
    prompt_tokens = completion_tokens = cached_tokens = 0
    cost_usd = 0.0
    events: Counter[str] = Counter()
    error_types: Counter[str] = Counter()
    models: Counter[str] = Counter()
    for line in lines:
        try:
            rec = json.loads(line)
        except ValueError:
            continue
        if not isinstance(rec, dict):
            continue
        event = str(rec.get("event", ""))
        if event == "extract complete":
            events["extract_complete"] += 1
            if isinstance(rec.get("elapsed_ms"), (int, float)):
                latencies.append(rec["elapsed_ms"] / 1000)
            usage = _usage(rec.get("usage"))
            cost_usd += float(usage.get("cost", 0) or 0)
            prompt_tokens += int(usage.get("prompt_tokens", 0))
            completion_tokens += int(usage.get("completion_tokens", 0))
            details = usage.get("prompt_tokens_details") or {}
            cached_tokens += int(usage.get("cached_tokens", 0) or (details.get("cached_tokens") or 0))
            models[str(rec.get("model", "?"))] += 1
        elif event == "worker error":
            events["worker_error"] += 1
            error_types[str(rec.get("error_type", "?"))] += 1
        elif event == "model call hung, retrying once":
            events["hung_retry"] += 1
        elif event == "primary model failed, using fallback":
            events["fallback_used"] += 1
        elif event == "invalid JSON from model, retrying once":
            events["invalid_json_retry"] += 1
    return {"latency_s": latencies, "prompt_tokens": prompt_tokens, "completion_tokens": completion_tokens,
            "cached_tokens": cached_tokens, "reported_cost_usd": round(cost_usd, 6), "events": dict(events), "error_types": dict(error_types),
            "models": dict(models)}


def token_cost(prompt_tokens: int, completion_tokens: int, usd_per_prompt_token: float,
               usd_per_completion_token: float) -> float:
    return prompt_tokens * usd_per_prompt_token + completion_tokens * usd_per_completion_token


def stratified_sample(ids_by_stratum: dict[str, list[str]], fraction: float, rng: Any) -> dict[str, list[str]]:
    """`fraction` of every stratum (at least one id), drawn with `rng` (random.Random) from the sorted ids."""
    sample: dict[str, list[str]] = {}
    for stratum, ids in sorted(ids_by_stratum.items()):
        k = max(1, math.ceil(len(ids) * fraction)) if ids else 0
        sample[stratum] = sorted(rng.sample(sorted(ids), k))
    return sample


def fill_table(states: Iterable[dict[str, Any]], field_names: Sequence[str]) -> dict[str, dict[str, Any]]:
    """Per state field: accounts where it is known vs unknown, and the standing that won it."""
    table: dict[str, dict[str, Any]] = {n: {"known": 0, "unknown": 0, "by_standing": Counter()} for n in field_names}
    for state in states:
        for name in field_names:
            field = (state.get("fields") or {}).get(name) or {}
            if field.get("known"):
                table[name]["known"] += 1
                table[name]["by_standing"][field.get("standing") or "derived"] += 1
            else:
                table[name]["unknown"] += 1
    return {n: {"known": v["known"], "unknown": v["unknown"], "by_standing": dict(v["by_standing"])}
            for n, v in table.items()}


def utf8_connection(url: str, **kwargs: Any) -> Any:
    """psycopg connection that reads text as UTF-8 whatever the database is labelled.

    The embedded Postgres a Windows machine initialises is labelled WIN1252, and the Go core stores the
    UTF-8 bytes of every string untouched (no client conversion), so a default client would show mojibake
    ('’' for an apostrophe) or fail on bytes like 0x81. A production database is UTF8 and
    needs none of this. Asking the server for SQL_ASCII turns off its conversion; the loaders then decode."""
    import psycopg
    from psycopg.adapt import Loader

    class Utf8Text(Loader):
        def load(self, data: Any) -> str:
            return bytes(data).decode("utf-8", "replace")

    conn = psycopg.connect(url, options="-c client_encoding=SQL_ASCII", **kwargs)
    for oid in ("text", "varchar", "name", "bpchar"):
        conn.adapters.register_loader(oid, Utf8Text)
    return conn


def canonical_stage(text: str, synonyms: dict[str, Any]) -> str | None:
    """The CRM stage a free-text stage names, by whole-word/phrase match ('x*' is a prefix); None if unknown."""
    words = " " + " ".join(_WORD.findall(text.lower())) + " "
    for stage in synonyms["order"]:
        for syn in synonyms["synonyms"][stage]:
            needle = syn[:-1] if syn.endswith("*") else None
            pattern = f" {needle}" if needle is not None else f" {syn} "
            if pattern in words:
                return stage
    return None


def stage_agrees_synonym(claimed: str, truth: str, synonyms: dict[str, Any]) -> bool:
    canon = canonical_stage(claimed, synonyms)
    return canon is not None and canon == canonical_stage(truth, synonyms)


def stage_in_force(events: Sequence[tuple[str, str]], at: str) -> str | None:
    """Stage of the latest (iso time, stage) event at or before `at`; None before the first event.
    ISO-8601 strings in one timezone compare correctly as text."""
    current = None
    for when, stage in sorted(events):
        if when > at:
            break
        current = stage
    return current


def chance_match_rate(stated: Sequence[float], deals: dict[str, Sequence[float]], own_deal: str, tolerance: float = 0.01) -> float:
    """Share of the OTHER deals whose CRM monetary values contain a value within `tolerance` of any stated amount:
    what a claim would score by agreeing with a randomly chosen deal."""
    others = [v for k, v in deals.items() if k != own_deal]
    if not others:
        return 0.0
    hits = sum(1 for values in others if any(amount_agrees(stated, v, tolerance) for v in values))
    return hits / len(others)


def claim_flags(row: dict[str, Any], synonyms: dict[str, Any], deal_values: dict[str, Sequence[float]]) -> dict[str, bool]:
    """Agreement flags of one claim row with the CRM values the row carries (a flag exists only when it can be judged)."""
    flags: dict[str, bool] = {}
    if "stage_claim" in row:
        claimed = row["stage_claim"]
        flags["stage_final_strict"] = stage_agrees(claimed, row["crm_stage_final"])
        flags["stage_final_synonyms"] = stage_agrees_synonym(claimed, row["crm_stage_final"], synonyms)
        if row.get("crm_stage_in_force"):
            flags["stage_in_force_strict"] = stage_agrees(claimed, row["crm_stage_in_force"])
            flags["stage_in_force_synonyms"] = stage_agrees_synonym(claimed, row["crm_stage_in_force"], synonyms)
    if "owner_claimed" in row:
        flags["owner"] = names_agree(row["owner_claimed"], row["crm_owner"])
    if row.get("crm_title"):
        flags["title_overlap"] = title_overlap(row["value"], row["crm_title"])
    stated = row.get("stated_amounts") or []
    if stated and row.get("crm_deal_id"):
        flags["amount_equals_deal_amount"] = amount_agrees(stated, float(row.get("crm_amount") or 0))
        flags["amount_in_deal_values"] = any(amount_agrees(stated, v) for v in deal_values.get(row["crm_deal_id"], ()))
    return flags
