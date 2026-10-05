"""Generation pipeline (spec §e/§f): WP31 export -> kernel -> rendered SourceEvents, labels and manifest.

Deterministic: the same export, rule file and seed give byte-identical files and the same manifest hash.
Nothing in the output depends on the wall clock or the machine. The base export is only read.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import asdict, replace
from datetime import datetime, timezone
from pathlib import Path
from collections.abc import Collection
from typing import Any, Mapping

from . import leakcheck, rules
from .base import Base, load
from .forward import DealResult, simulate
from .plan import make_plan
from .paraphrase import Paraphraser
from .power_sim import evaluate, gate_failures
from .records import PROVENANCE, mark
from .render_all import ROLE_C, DealRender, Ids, _crm, render_deal
from .seeding import stream
from .world import WorldDeal

EPOCH = datetime(2020, 1, 1, tzinfo=timezone.utc)
ATTRIBUTION = ("Base data: CRMArena-Pro B2B Salesforce org by Salesforce AI Research (third-party synthetic data), "
               "CC BY-NC 4.0, non-commercial use only. Synthetic layer: WP32 synthetic:v1 (labelled, generated).")


def simulate_base(base: Base, rule_set: rules.RuleSet, proc: Mapping[str, float], seed: int) -> list[DealResult]:
    """Each account's deals in creation order; a deal also sees the synthetic Quote entries of its earlier deals."""
    seen: dict[str, list[float]] = {}
    out = []
    for bd in base.deals:
        start = (bd.start - EPOCH).total_seconds() / 86400
        mine = seen.setdefault(bd.deal.account_id, [])
        deal = replace(bd.deal, account_quote_days=tuple(sorted(bd.deal.account_quote_days + tuple(q - start for q in mine))))
        res = simulate(deal, rule_set, proc, seed)
        if res.quote_t is not None:
            mine.append(start + res.quote_t)
        out.append(res)
    return out


def real_world(base: Base) -> list[WorldDeal]:
    return [WorldDeal(bd.deal, (bd.start - EPOCH).total_seconds() / 86400, bd.nulls, None) for bd in base.deals]


def evaluate_real(export: Path, rule_set: rules.RuleSet, proc: Mapping[str, float], seed: int,
                  noise: Mapping[str, Any] | None = None):
    """The gated metrics of one seed on the REAL base (the CRMArena export), clean or noisy (power_sweep --base)."""
    base = load(export, proc, seed, float(rule_set.outcome["account_effect_sd"]))
    return evaluate(seed, rule_set, real_world(base), simulate_base(base, rule_set, proc, seed), noise)


def _role_events(dr: DealRender, s, first_deal: bool) -> None:
    """At an account's first deal, half of the role assignments surface as CRM Role__c changes (spec §a)."""
    if not first_deal:
        return
    for cid, role in dr.bd.roles:
        if s.uniform() < 0.5 and cid in dr.contacts:
            at = dr.at(0.0)
            _crm(dr, at, "Contact", f"contact:{cid}", f"field:Role__c:{ROLE_C[role]}", {"Role__c": ROLE_C[role]})


def _repeated_title(event: Mapping[str, Any]) -> bool:
    """A Contact Title change whose identity (contact, new title) was already emitted: shared contacts move to the
    same role in several deals of an account. The first one stands; any other collision is a bug."""
    return event["source_system"] == "crm" and event["source_event_key"].startswith("field:Title:")


def _write(path: Path, text: str) -> str:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8", newline="\n")
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def _jsonl(rows: list[dict[str, Any]]) -> str:
    return "".join(json.dumps(mark(r), sort_keys=True, ensure_ascii=False) + "\n" for r in rows)


def generate_to(export: Path, out: Path, rule_set: rules.RuleSet, doc: Mapping[str, Any],
                paraphrase: tuple[str, Path] | None = None, paraphrase_vocab: Collection[str] = (),
                allow_template_fallback: bool = False) -> dict[str, Any]:
    proc, seed = dict(doc["process"]), rule_set.generation_seed
    base = load(export, proc, seed, float(rule_set.outcome["account_effect_sd"]))
    results = simulate_base(base, rule_set, proc, seed)
    para = Paraphraser(paraphrase[0], paraphrase[1], seed, base.export_sha256[:12], allow_fallback=allow_template_fallback,
                       vocab=paraphrase_vocab) if paraphrase else None
    ids, files, seen_accounts = Ids(base.quote_number_max), {}, set()
    deals_rows, triples, index, repeated_titles = [], set(), [], 0
    for bd, res in zip(base.deals, results):
        dr = DealRender(bd, res, ids, stream(seed, "render", bd.deal.deal_id), dict(base.users), dict(base.contacts), para=para)
        plan = make_plan(bd.deal, proc, stream(seed, "deal", bd.deal.deal_id, "exogenous"))
        _role_events(dr, stream(seed, "render", bd.deal.account_id, "roles"), bd.deal.account_id not in seen_accounts)
        seen_accounts.add(bd.deal.account_id)
        events = render_deal(dr, {c.id: c.function for c in plan.new_contacts})
        kept = []
        for e in events:
            key = (e["source_system"], e["source_object_id"], e["source_event_key"])
            if key in triples:
                if not _repeated_title(e):
                    raise RuntimeError(f"identity collision among synthetic events: {key}")
                repeated_titles += 1  # the contact already holds this title from an earlier deal's move
                continue
            triples.add(key)
            kept.append(e)
        events = kept
        rel = f"events/{bd.deal.account_id}/{bd.deal.deal_id}.json"
        index += [{"source_system": e["source_system"], "source_object_id": e["source_object_id"],
                   "source_event_key": e["source_event_key"], "occurred_at": e["occurred_at"], "file": rel,
                   "account": bd.deal.account_id, "deal": bd.deal.deal_id} for e in events]
        files[rel] = _write(out / rel, json.dumps(events, sort_keys=True, ensure_ascii=False, indent=1) + "\n")
        deals_rows.append({"deal": bd.deal.deal_id, "account": bd.deal.account_id, "won": res.won, "outcome_source": res.outcome_source,
                           "dormant": res.dormant, "close_day": res.close_t, "features": dict(res.features),
                           "roles": dict(bd.roles)})
    covered = sorted(r["deal"] for r in deals_rows if r["outcome_source"] == "synthetic")
    # HAR-129 demo manifest: base-vs-synthetic provenance of every event. Every identity listed here is
    # synthetic:v1; any other event of a replay is base. No ground truth here (that stays in labels/).
    files["index/events.jsonl"] = _write(out / "index/events.jsonl", _jsonl(index))
    files["labels/deals.jsonl"] = _write(out / "labels/deals.jsonl", _jsonl(deals_rows))
    role_rows = [{"deal": r["deal"], "account": r["account"], "contact": c, "role": role}
                 for r in deals_rows for c, role in sorted(r["roles"].items())]  # the champion is per deal
    files["labels/roles.jsonl"] = _write(out / "labels/roles.jsonl", _jsonl(role_rows))
    files["labels/covered_deals.json"] = _write(out / "labels/covered_deals.json",
                                                json.dumps({"provenance": PROVENANCE, "deals": covered}, indent=1) + "\n")
    problems = leakcheck.scan_generated(out / "events", rule_set)
    if problems:
        raise RuntimeError("generated events leak:\n" + "\n".join(problems[:20]))
    world = real_world(base)
    realised = evaluate(seed, rule_set, world, results) if len(results) >= 50 else None
    manifest = {
        "provenance": PROVENANCE, "attribution": ATTRIBUTION, "rule_set_version": rule_set.version,
        "rules_sha256": rules.content_hash(doc), "frozen_sha256": doc.get("frozen_sha256"), "status": rule_set.status,
        "generation_seed": seed, "base_export_sha256": base.export_sha256, "deals": len(results),
        "covered_deals": len(covered), "real_outcome_deals": len(results) - len(covered),
        "events": len(triples), "repeated_contact_title_events_dropped": repeated_titles, "files": dict(sorted(files.items())),
        "paraphrase": ({"mode": para.mode, "style_seed": para.seed, "stats": dict(para.stats),
                        "allow_template_fallback": para.allow_fallback,
                        "cassettes_sha256": para.cassettes_sha256() if para.mode == "replay" else None,
                        "requests_on_file": para.flush_requests() if para.mode == "collect" else None} if para else None),
        "realised": asdict(realised) if realised else None,
        "realised_gate_failures": gate_failures(realised, rule_set) if realised else None,
    }
    text = json.dumps(manifest, sort_keys=True, indent=1) + "\n"
    _write(out / "manifest.json", text)
    manifest["manifest_sha256"] = hashlib.sha256(text.encode("utf-8")).hexdigest()
    return manifest
