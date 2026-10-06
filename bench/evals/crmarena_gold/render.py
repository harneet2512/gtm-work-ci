"""Blind labelling sheets (HAR-114 gold v2): the situation and the candidate as a labeller sees them.

A sheet shows the context, the candidate action, and the eval types to judge with their catalog label sets and blocking
rules. It never shows the case type, the title, the design intent (should_pass), the best action or the notes.
"""
from __future__ import annotations

import json
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[3]
CATALOG = json.loads((ROOT / "contracts" / "evals" / "eval_catalog.json").read_text(encoding="utf-8"))["eval_types"]


def names(case: dict[str, Any]) -> dict[str, str]:
    group = case["context"]["state"]["buying_group"]
    out = {m["person_id"]: f"{m['display_name']} ({m.get('title') or 'no title'})" for m in group}
    return out


def person(pid: str | None, lookup: dict[str, str]) -> str:
    return lookup.get(pid or "", "rep" if pid else "n/a")


def activity_block(a: dict[str, Any], lookup: dict[str, str]) -> str:
    head = f"[{a['occurred_at']}] {a['activity_type']} ({a.get('date_semantics', '')}) from {person(a['actor_person_id'], lookup)} | {a.get('subject', '')}"
    return f"{head}\n{a['text']}"


def eval_block(name: str) -> str:
    e = CATALOG[name]
    labels = ", ".join(e["labels"]) if e["labels"] else "(no label)"
    rule = e.get("blocking_rule") or ("never blocks" if not e["can_block"] else "deterministic: may block")
    return f"- {name}: {e['description']}\n    labels: {labels}\n    blocking rule: {rule}"


def sheet(case: dict[str, Any], evals: list[str], sheet_id: str | None = None) -> str:
    ctx, cand = case["context"], case["candidate_action"]
    lookup = names(case)
    state = ctx["state"]
    lines = [f"SHEET {sheet_id or case['id']}", f"NOW: {ctx['now']}", "", "ACCOUNT STATE (derived from events up to NOW)",
             json.dumps(state["fields"], indent=1, ensure_ascii=False),
             "buying group: " + "; ".join(f"{m['display_name']} / {m.get('title')} / roles {m['roles']} / {m['status']}" for m in state["buying_group"]),
             f"coverage gaps: {state['coverage_gaps']}", "", f"RECENT CHANGES: {ctx['recent_changes']['summary']} signals={ctx['recent_changes']['signals']}",
             "", "TRIGGER", activity_block(ctx["trigger"], lookup), "", "SUPPORTING ACTIVITIES"]
    lines += [activity_block(a, lookup) + "\n" for a in ctx["supporting_activities"]]
    lines += ["COMMITMENTS: " + json.dumps(ctx["commitments"], ensure_ascii=False), "TIMELINE: " + json.dumps(ctx["timeline_facts"], ensure_ascii=False)]
    for k in ctx["offered_knowledge"]:
        lines += ["", f"OFFERED KNOWLEDGE {k['key']}: {k['title']}", "  signature: " + json.dumps(k["situation_signature"], ensure_ascii=False),
                  "  guidance: " + json.dumps(k["guidance"], ensure_ascii=False), "  exceptions: " + json.dumps(k["exceptions"], ensure_ascii=False)]
    art = cand["finished_artifact"]
    to = ", ".join(f"{r['role']}: {person(r['person_id'], {**lookup})}" for r in cand["recipients"]) or "(none)"
    lines += ["", "CANDIDATE ACTION", f"type: {cand['proposed_action_type']}  channel: {art['channel']}  recipients: {to}",
              f"subject: {art.get('subject')}", f"attachments: {art.get('attachments', [])}", "body:", art["body"],
              f"next step: {cand['crm_next_step_intent']}", f"reason: {cand['reason']}", f"wait_until: {cand.get('wait_until')}",
              f"knowledge used: {cand.get('knowledge_refs_used', [])}",
              "evidence cited: " + json.dumps([r.get("quote") for r in cand["evidence_refs"]], ensure_ascii=False), "",
              "JUDGE THESE EVALS (verdict pass|warn|fail|abstain, label if the eval has labels, blocking only if the rule is met):"]
    lines += [eval_block(e) for e in evals]
    return "\n".join(lines)
