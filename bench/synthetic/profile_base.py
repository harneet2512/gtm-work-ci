"""Aggregate profile of the WP31 CRMArena-Pro export for the synthetic layer (no records copied).

    python -m synthetic.profile_base <export dir>      (from bench/; writes base_profile.v1.json)

Only aggregates leave the export: the title seniority/function mix (via roles.parse_title), the
customer-email share, emails per deal, body-length quantiles, and per-prefix id facts (the max id
body, so synthetic ids are allocated after it, and the typical gap between consecutive ids).
"""
from __future__ import annotations

import json
import sys
from collections import Counter
from pathlib import Path
from typing import Any

from .records import BASE62, id_number
from .roles import parse_title

HERE = Path(__file__).resolve().parent
PROFILE_PATH = HERE / "base_profile.v1.json"


def _load(export: Path, name: str) -> list[dict[str, Any]]:
    return json.loads((export / f"{name}.json").read_text(encoding="utf-8"))


def _quantiles(values: list[int], qs: tuple[float, ...] = (0.1, 0.5, 0.9)) -> list[int]:
    v = sorted(values)
    return [v[int(q * (len(v) - 1))] for q in qs]


def _mix(values: Any) -> dict[str, float]:
    counts = Counter(values)
    total = sum(counts.values())
    return {k: round(v / total, 4) for k, v in sorted(counts.items())}


def _id_facts(ids: list[str]) -> dict[str, Any]:
    nums = sorted(id_number(i) for i in ids)
    gaps = [b - a for a, b in zip(nums, nums[1:]) if b > a]
    return {"count": len(ids), "max_body": ids and max(ids, key=id_number)[3:15], "median_gap": _quantiles(gaps)[1] if gaps else 1}


def profile(export: Path) -> dict[str, Any]:
    contacts, emails = _load(export, "Contact"), _load(export, "EmailMessage")
    users = {u["Email"].lower() for u in _load(export, "User") if u.get("Email")}
    parsed = [parse_title(c.get("Title")) for c in contacts]
    customer = [e for e in emails if (e.get("FromAddress") or "").lower() not in users]
    per_deal = Counter(e.get("RelatedToId") for e in emails)
    by_prefix: dict[str, list[str]] = {}
    for name in ("Contact", "Opportunity", "EmailMessage", "Task", "Quote"):
        for rec in _load(export, name):
            by_prefix.setdefault(rec["Id"][:3], []).append(rec["Id"])
    n = len(parsed)
    quotes, opps = _load(export, "Quote"), _load(export, "Opportunity")
    won = {q["OpportunityId"] for q in quotes if q.get("Status") == "Accepted"} | {o["Id"] for o in opps if o.get("ContractId__c")}
    lost = {q["OpportunityId"] for q in quotes if q.get("Status") in ("Rejected", "Denied")} - won
    return {
        "real_terminal_won_share": round(len(won) / len(opps), 4),
        "real_terminal_lost_share": round(len(lost) / len(opps), 4),
        "quotes_per_opportunity": round(len(quotes) / len(opps), 4),
        "description": "Aggregates of the WP31 CRMArena-Pro export (profile_base.py). No record is copied.",
        "title_filled_share": round(sum(1 for c in contacts if c.get("Title")) / n, 4),
        "seniority_mix": {str(k): round(v / n, 4) for k, v in sorted(Counter(s for s, _ in parsed).items())},
        "function_mix": {k: round(v / n, 4) for k, v in sorted(Counter(f for _, f in parsed).items())},
        "customer_email_share": round(len(customer) / len(emails), 4),
        "emails_per_deal": _quantiles(list(per_deal.values())),
        "customer_body_chars": _quantiles([len(e.get("TextBody") or "") for e in customer]),
        "email_hour_mix": _mix(e["MessageDate"][11:13] for e in emails),
        "email_minute_mix": _mix(e["MessageDate"][14:16] for e in emails),
        "ids": {p: _id_facts(v) for p, v in sorted(by_prefix.items())},
        "alphabet": BASE62,
    }


def main(argv: list[str]) -> int:
    PROFILE_PATH.write_text(json.dumps(profile(Path(argv[0])), indent=1) + "\n", encoding="utf-8")
    print(PROFILE_PATH.read_text(encoding="utf-8"))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
