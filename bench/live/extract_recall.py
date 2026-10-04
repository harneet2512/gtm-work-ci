"""Live extraction benchmark: how many gold critical facts does /v1/extract recover?

For every fixture event that a gold checkpoint cites as evidence for a critical fact, run the real
extractor (OpenRouter via litellm, GHOST_MODEL) REPEATS times and check whether a claim with the
matching field and an overlapping verbatim quote comes back.

    OPENROUTER_API_KEY=... python bench/live/extract_recall.py [--repeats 3] [--out bench/reports]
    python bench/live/extract_recall.py --rescore bench/reports/live-extract-<date>.json   # no model calls

Metrics: fact recall (field + quote), fold recall (claim folds into the same AccountState field, per
claim.v1.json#/$defs/fieldPath), quote-only recall, consistency across repeats (recalled in every run
vs at least one), per-field recall, misses by extracted field, claims per event, dropped quotes.
Fixtures are synthetic (see NOTICE); no customer data is sent anywhere.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
import time
import uuid
from collections import Counter, defaultdict
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "worker-py"))

from ghost_worker.extract.service import extract_claims  # noqa: E402
from ghost_worker.llm.factory import build_extract_provider  # noqa: E402
from ghost_worker.models import ExtractRequest  # noqa: E402
from ghost_worker.settings import Settings  # noqa: E402

FIXTURES = ROOT / "fixtures"
# AccountState field (gold) -> claim field_paths that legitimately carry it.
FIELD_PATHS = {
    "current_commitments": {"commitment"},
    "buying_group": {"buying_group.member", "stakeholder_role", "champion", "economic_buyer"},
    "champion": {"champion", "buying_group.member", "stakeholder_role"},
    "champion_status": {"champion_status", "delegation"},
    "economic_buyer": {"economic_buyer", "stakeholder_role", "buying_group.member"},
    "person.title": {"stakeholder_role", "buying_group.member"},
}


def body_text(payload: dict) -> str | None:
    """Body text per contracts/normalization.md."""
    kind = payload.get("kind")
    if kind == "email":
        return payload.get("body_text")
    if kind == "call" and payload.get("transcript"):
        return "\n".join(f"{seg['speaker']}: {seg['text']}" for seg in payload["transcript"])
    if kind == "slack_message":
        return payload.get("text")
    if kind == "crm_change":
        return payload.get("note_body")
    return None


def participants(payload: dict) -> list[dict]:
    if payload.get("kind") == "email":
        out = [{"raw_identity": payload["from"]["email"].lower(), "display_name": payload["from"].get("name"), "role": "from"}]
        for role in ("to", "cc"):
            out += [{"raw_identity": a["email"].lower(), "display_name": a.get("name"), "role": role}
                    for a in payload.get(role, [])]
        return out
    if payload.get("kind") == "call":
        return [{"raw_identity": f"call:{payload['call_id']}:{s['label']}", "display_name": s.get("name"), "role": "speaker"}
                for s in payload.get("speakers", [])]
    if payload.get("kind") == "slack_message":
        return [{"raw_identity": f"slack:{payload['user']}", "role": "actor"}]
    return []


ACTIVITY_TYPE = {"email": "EmailReceived", "call": "TranscriptReady", "slack_message": "SlackMessage",
                 "crm_change": "CRMNoteAdded"}


def activity(event_file: str, event: dict) -> dict:
    p = event["payload"]
    digest = hashlib.sha256(event_file.encode()).hexdigest()
    now = datetime.now(timezone.utc).isoformat()
    return {
        "id": str(uuid.uuid5(uuid.NAMESPACE_URL, "act:" + event_file)),
        "idempotency_key": digest, "activity_type": ACTIVITY_TYPE[p["kind"]],
        "source_system": event["source_system"], "source_object_id": event["source_object_id"],
        "source_event_id": str(uuid.uuid5(uuid.NAMESPACE_URL, "se:" + event_file)),
        "occurred_at": event.get("occurred_at") or now, "ingested_at": now,
        "participants": participants(p), "payload_ref": "source_events/bench",
        "permissions": {"visibility": "org"},
        "provenance": {"source_system": event["source_system"], "source_object_id": event["source_object_id"]},
    }


def org_domain() -> str:
    org = json.loads((FIXTURES / "world" / "org.json").read_text(encoding="utf-8"))
    return org["organization"]["domain"].lower()


def known_people(account: str) -> list[dict]:
    """Gold people with an e-mail identity; `internal` (seller side) from the org domain, as core would mark it."""
    gold = json.loads((FIXTURES / "gold" / account / "cp4.json").read_text(encoding="utf-8"))
    ours = "@" + org_domain()
    out = []
    for ent in gold["expected"]["entities"]:
        if ent["kind"] != "person":
            continue
        for ident in ent["source_identities"]:
            if ident.startswith("email:"):
                email = ident[len("email:"):]
                out.append({"raw_identity": email, "display_name": ent["display_name"],
                            "internal": email.lower().endswith(ours)})
    return out


def gold_facts() -> list[dict]:
    facts, seen = [], set()
    for path in sorted((FIXTURES / "gold").glob("*/cp*.json")):
        gold = json.loads(path.read_text(encoding="utf-8"))
        for f in gold["expected"].get("critical_facts", []):
            if not f.get("evidence_quote"):
                continue
            key = (f["evidence_event_file"], f["field"], f["evidence_quote"])
            if key not in seen:
                seen.add(key)
                facts.append({**f, "account": gold["account"]})
    return facts


def _norm(s: str) -> str:
    return re.sub(r"\s+", " ", s).strip().lower()


def quote_match(candidate: str, gold: str) -> bool:
    c, g = _norm(candidate), _norm(gold)
    if c in g or g in c:
        return True
    ct, gt = set(re.findall(r"\w+", c)), set(re.findall(r"\w+", g))
    return bool(ct and gt) and len(ct & gt) / len(ct | gt) >= 0.5


# claim field_path -> AccountState field it folds into (claim.v1.json#/$defs/fieldPath).
FOLDS = {"commitment": "current_commitments", "buying_group.member": "buying_group",
         "stakeholder_role": "buying_group", "delegation": "buying_group"}


def fold(field: str) -> str:
    return FOLDS.get(field, field)


def recalled(fact: dict, claims: list[dict], mode: str) -> bool:
    """mode: 'field' (gold field or a listed synonym), 'fold' (same AccountState field), 'quote' (any)."""
    allowed = FIELD_PATHS.get(fact["field"], {fact["field"]})
    for c in claims:
        if not quote_match(c["evidence_quote"], fact["evidence_quote"]):
            continue
        if mode == "quote" or c["field_path"] in allowed:
            return True
        if mode == "fold" and fold(c["field_path"]) == fold(fact["field"]):
            return True
    return False


def missed_as(fact: dict, claims: list[dict]) -> list[str]:
    """Field paths the extractor gave this fact's quote ('<no quote>' when it did not extract it)."""
    used = [c["field_path"] for c in claims if quote_match(c["evidence_quote"], fact["evidence_quote"])]
    return used or ["<no quote>"]


def _extract_once(ef: str, req, provider) -> tuple[str, dict, dict]:
    t0 = time.monotonic()
    try:
        resp = extract_claims(req, provider)
        claims = [c.model_dump(mode="json") for c in resp.claims]
        st = {"claims": len(claims), "dropped": resp.dropped, "rejected": resp.rejected, "errors": 0}
    except Exception as exc:  # noqa: BLE001 - benchmark records failures instead of stopping
        claims, st = [], {"claims": 0, "dropped": 0, "rejected": 0, "errors": 1}
        print(f"error on {ef}: {type(exc).__name__}", file=sys.stderr)
    st["seconds"] = time.monotonic() - t0
    return ef, {"claims": claims, "seconds": round(st["seconds"], 1)}, st


def run(repeats: int, out_dir: Path, workers: int = 8) -> dict:
    settings = Settings()
    provider = build_extract_provider(settings)  # extract_deadline_s, as /v1/extract uses
    facts = gold_facts()
    events = sorted({f["evidence_event_file"] for f in facts})
    jobs = []
    for ef in events:
        event = json.loads((FIXTURES / ef).read_text(encoding="utf-8"))
        text = body_text(event["payload"])
        if not text:
            continue
        account = ef.split("/")[2] if ef.startswith("world/") else ef.split("/")[1].split("_")[0]
        req = ExtractRequest.model_validate({"activity": activity(ef, event), "text": text,
                                             "known_people": known_people(account)})
        jobs += [(ef, req)] * repeats
    results: dict[str, list[dict]] = defaultdict(list)
    stats: dict = {"calls": 0, "errors": 0, "claims": 0, "dropped": 0, "rejected": 0, "seconds": 0.0,
                   "max_seconds": 0.0}
    with ThreadPoolExecutor(max_workers=workers) as pool:
        for ef, run_result, st in pool.map(lambda j: _extract_once(j[0], j[1], provider), jobs):
            results[ef].append(run_result)
            stats["calls"] += 1
            for k in ("errors", "claims", "dropped", "rejected", "seconds"):
                stats[k] += st[k]
            stats["max_seconds"] = max(stats["max_seconds"], st["seconds"])
    per_fact = score(facts, results)
    report = summarize(per_fact, stats, repeats, settings.ghost_model)
    stamp = datetime.now(timezone.utc).strftime("%Y-%m-%d")
    write(out_dir / f"live-extract-{stamp}", report, per_fact, results, stats)
    return report


def score(facts: list[dict], results: dict[str, list[dict]]) -> list[dict]:
    per_fact = []
    for f in facts:
        runs = results.get(f["evidence_event_file"], [])
        misses: Counter[str] = Counter()
        for r in runs:
            if not recalled(f, r["claims"], "field"):
                misses.update(missed_as(f, r["claims"]))
        per_fact.append({"account": f["account"], "event": f["evidence_event_file"], "field": f["field"],
                         "quote": f["evidence_quote"], "runs": len(runs),
                         "field_hits": sum(recalled(f, r["claims"], "field") for r in runs),
                         "fold_hits": sum(recalled(f, r["claims"], "fold") for r in runs),
                         "quote_hits": sum(recalled(f, r["claims"], "quote") for r in runs),
                         "missed_as": dict(misses)})
    return per_fact


def write(stem: Path, report: dict, per_fact: list[dict], results: dict, stats: dict) -> None:
    stem.parent.mkdir(parents=True, exist_ok=True)
    payload = {"summary": report, "stats": stats, "facts": per_fact, "raw": results}
    Path(f"{stem}.json").write_text(json.dumps(payload, indent=2, ensure_ascii=False) + "\n",
                                    encoding="utf-8", newline="\n")
    Path(f"{stem}.md").write_text(markdown(report, per_fact), encoding="utf-8", newline="\n")


def rescore(path: Path) -> dict:
    """Re-apply the current scorer to a saved run without model calls; call stats come from that run."""
    saved = json.loads(path.read_text(encoding="utf-8"))
    old = saved["summary"]
    calls = old["calls"]
    stats = saved.get("stats") or {"calls": calls, "errors": old["errors"],
                                   "claims": old["claims_per_call"] * calls,
                                   "dropped": old["dropped_per_call"] * calls,
                                   "seconds": old["seconds_per_call"] * calls,
                                   "max_seconds": old["max_seconds_per_call"]}
    per_fact = score(gold_facts(), saved["raw"])
    report = summarize(per_fact, stats, old["repeats"], old["model"])
    write(path.with_suffix(""), report, per_fact, saved["raw"], stats)
    return report


def summarize(per_fact: list[dict], stats: dict, repeats: int, model: str) -> dict:
    measured = [f for f in per_fact if f["runs"]]
    n = len(measured) or 1
    by_field: dict[str, list[int]] = defaultdict(lambda: [0, 0])
    for f in measured:
        by_field[f["field"]][0] += f["field_hits"]
        by_field[f["field"]][1] += f["runs"]
    return {
        "model": model, "repeats": repeats, "facts": len(measured), "calls": stats["calls"], "errors": stats["errors"],
        "fact_recall": round(sum(f["field_hits"] for f in measured) / max(1, sum(f["runs"] for f in measured)), 3),
        "fold_recall": round(sum(f["fold_hits"] for f in measured) / max(1, sum(f["runs"] for f in measured)), 3),
        "quote_recall": round(sum(f["quote_hits"] for f in measured) / max(1, sum(f["runs"] for f in measured)), 3),
        "recalled_every_run": round(sum(f["field_hits"] == f["runs"] for f in measured) / n, 3),
        "recalled_at_least_once": round(sum(f["field_hits"] > 0 for f in measured) / n, 3),
        "claims_per_call": round(stats["claims"] / max(1, stats["calls"]), 2),
        "dropped_per_call": round(stats["dropped"] / max(1, stats["calls"]), 3),
        "rejected_per_call": round(stats.get("rejected", 0) / max(1, stats["calls"]), 3),  # absent in old runs
        "seconds_per_call": round(stats["seconds"] / max(1, stats["calls"]), 2),
        "max_seconds_per_call": round(stats["max_seconds"], 1),
        "per_field_recall": {k: round(v[0] / v[1], 3) for k, v in sorted(by_field.items())},
        "missed_as": {k: dict(c.most_common()) for k, c in sorted(_misses_by_field(measured).items())},
    }


def _misses_by_field(per_fact: list[dict]) -> dict[str, Counter[str]]:
    out: dict[str, Counter[str]] = defaultdict(Counter)
    for f in per_fact:
        out[f["field"]].update(f["missed_as"])
    return out


def markdown(report: dict, per_fact: list[dict]) -> str:
    lines = [f"# Live extraction benchmark ({report['model']}, {report['repeats']} runs per event)", "",
             "| Metric | Value |", "|---|---|"]
    for k in ("facts", "calls", "errors", "fact_recall", "fold_recall", "quote_recall", "recalled_every_run",
              "recalled_at_least_once", "claims_per_call", "dropped_per_call", "rejected_per_call",
              "seconds_per_call",
              "max_seconds_per_call"):
        lines.append(f"| {k} | {report[k]} |")
    lines += ["", "## Recall by field", "", "| Field | Recall |", "|---|---|"]
    lines += [f"| {k} | {v} |" for k, v in report["per_field_recall"].items()]
    lines += ["", "## Misses by what the extractor did instead", "", "| Gold field | Extracted as (runs) |",
              "|---|---|"]
    lines += [f"| {k} | {', '.join(f'{f} ×{n}' for f, n in v.items())} |" for k, v in report["missed_as"].items()]
    misses = [f for f in per_fact if f["runs"] and f["field_hits"] == 0]
    lines += ["", f"## Never recalled ({len(misses)})", "", "| Account | Field | Event | Quote |", "|---|---|---|---|"]
    lines += [f"| {m['account']} | {m['field']} | {m['event'].rsplit('/', 1)[-1]} | {m['quote'][:90]} |" for m in misses]
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--repeats", type=int, default=3)
    ap.add_argument("--out", type=Path, default=ROOT / "bench" / "reports")
    ap.add_argument("--workers", type=int, default=8)
    ap.add_argument("--rescore", type=Path, help="re-score a saved live-extract JSON without model calls")
    args = ap.parse_args()
    report = rescore(args.rescore) if args.rescore else run(args.repeats, args.out, args.workers)
    print(json.dumps(report, indent=2))
