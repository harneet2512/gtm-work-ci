"""Live account-agent benchmark: does Decision Guidance change Draft 1 (HAR-97 §16)?

Runs the real /v1/draft use case (OpenRouter via litellm, GHOST_MODEL) against the fake core and
context packets from worker-py/tests/draft_situations.py, REPEATS times per situation, and records
the action, recipients, cited knowledge and failures.

    OPENROUTER_API_KEY=... python bench/live/agent_causality.py [--repeats 5] [--out bench/reports]

The situations' context packets are synthetic test data. This measures model behaviour on fixed
context; end-to-end runs on real core context come after WP8/WP9.
"""
from __future__ import annotations

import argparse
import json
import sys
import time
from collections import Counter, defaultdict
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from pathlib import Path
from typing import get_args

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "worker-py"))
sys.path.insert(0, str(ROOT / "worker-py" / "tests"))

from draft_doubles import CORE_URL, RUN_TOKEN, FakeCore  # noqa: E402
from draft_situations import ELENA, K17, MARCO, PRIYA, SAM, SITUATIONS  # noqa: E402

from ghost_worker.draft.core_client import CoreContextClient, ToolName  # noqa: E402
from ghost_worker.draft.deadline import Deadline  # noqa: E402
from ghost_worker.draft.service import draft_action  # noqa: E402
from ghost_worker.llm.factory import build_provider  # noqa: E402
from ghost_worker.models.draft import DraftRequest  # noqa: E402
from ghost_worker.settings import Settings  # noqa: E402


def core_packets(situation) -> dict[str, list[dict]]:
    """Every context tool answers, like real core: tools the situation did not script return no items."""
    return {tool: situation.packets.get(tool, []) for tool in get_args(ToolName)}


def one_run(situation, provider, settings: Settings) -> dict:
    t0 = time.monotonic()
    fake = FakeCore(core_packets(situation))
    request = DraftRequest.model_validate(situation.request_copy())
    try:
        with CoreContextClient(CORE_URL, RUN_TOKEN, timeout_s=settings.core_timeout_s, max_bytes=16384,
                               transport=fake.transport) as core:
            resp = draft_action(request, provider, core, deadline=Deadline(settings.draft_deadline_s))
    except Exception as exc:  # noqa: BLE001 - benchmark records failures instead of stopping
        return {"error": getattr(exc, "code", type(exc).__name__), "message": str(exc)[:300],
                "seconds": round(time.monotonic() - t0, 1), "pulled": pulled(fake)}
    out = resp.output
    recipients = {r.person_id: r.role for r in out.recipients}
    return {"action": out.proposed_action_type, "recipients": recipients, "seconds": round(time.monotonic() - t0, 1),
            "knowledge": list(out.knowledge_refs_used), "used_guidance": resp.decision.used_guidance,
            "tool_calls": resp.tool_calls, "pulled": pulled(fake)}


def pulled(fake: FakeCore) -> list[str]:
    """Each core pull with its arguments (path?query), so repeated vs distinct calls are visible in the report."""
    return [r.url.path + (f"?{r.url.query.decode()}" if r.url.query else "") for r in fake.requests]


CHECKS = {
    "acme_unguided": lambda r: {"priya_included": PRIYA in r.get("recipients", {}),
                                "marco_included": MARCO in r.get("recipients", {})},
    "acme_guided_k17": lambda r: {"priya_included": PRIYA in r.get("recipients", {}),
                                  "marco_included": MARCO in r.get("recipients", {}),
                                  "k17_cited": K17 in r.get("knowledge", [])},
    "acme_guided_wait_until_tuesday": lambda r: {"waited": r.get("action") == "wait",
                                                 "knowledge_kept": bool(r.get("knowledge"))},
    "beta_non_material": lambda r: {"no_action": r.get("action") == "no_action"},
    "northstar_delegation_exception": lambda r: {"sam_included": SAM in r.get("recipients", {}),
                                                 "elena_not_forced": ELENA not in r.get("recipients", {}),
                                                 "k17_not_cited": K17 not in r.get("knowledge", [])},
}


def run(repeats: int, out_dir: Path, workers: int = 6) -> dict:
    settings = Settings()
    provider = build_provider(settings)
    raw: dict[str, list[dict]] = defaultdict(list)
    jobs = [(name, situation) for name, situation in SITUATIONS.items() for _ in range(repeats)]
    with ThreadPoolExecutor(max_workers=workers) as pool:
        for name, result in pool.map(lambda j: (j[0], one_run(j[1], provider, settings)), jobs):
            raw[name].append(result)
    summary = {}
    for name, runs in raw.items():
        ok = [r for r in runs if "error" not in r]
        checks: dict[str, float] = {}
        for r in ok:
            for k, v in CHECKS[name](r).items():
                checks[k] = checks.get(k, 0) + int(v)
        summary[name] = {"runs": len(runs), "errors": Counter(r["error"] for r in runs if "error" in r),
                         "actions": Counter(r["action"] for r in ok),
                         "checks": {k: f"{int(v)}/{len(ok)}" for k, v in checks.items()}}
    report = {"model": settings.ghost_model, "repeats": repeats, "situations": summary}
    stamp = datetime.now(timezone.utc).strftime("%Y-%m-%d")
    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / f"live-agent-{stamp}.json").write_text(
        json.dumps({"summary": report, "raw": raw}, indent=2, ensure_ascii=False, default=dict) + "\n",
        encoding="utf-8", newline="\n")
    (out_dir / f"live-agent-{stamp}.md").write_text(markdown(report), encoding="utf-8", newline="\n")
    return report


def markdown(report: dict) -> str:
    lines = [f"# Live account-agent benchmark ({report['model']}, {report['repeats']} runs per situation)", "",
             "| Situation | Actions | Checks | Errors |", "|---|---|---|---|"]
    for name, s in report["situations"].items():
        actions = ", ".join(f"{a}×{n}" for a, n in s["actions"].items()) or "-"
        checks = ", ".join(f"{k} {v}" for k, v in s["checks"].items()) or "-"
        errors = ", ".join(f"{e}×{n}" for e, n in s["errors"].items()) or "0"
        lines.append(f"| {name} | {actions} | {checks} | {errors} |")
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--repeats", type=int, default=5)
    ap.add_argument("--out", type=Path, default=ROOT / "bench" / "reports")
    ap.add_argument("--workers", type=int, default=6)
    args = ap.parse_args()
    print(json.dumps(run(args.repeats, args.out, args.workers), indent=2, default=dict))
