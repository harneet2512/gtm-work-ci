"""WP32 (HAR-131) v1.1, review HIGH 1: a synthetic reply is rendered through exactly the WP31 base scheme.

This test renders a reply to a REAL base email of fixtures/crmarena_sample and pins it as
fixtures/synthetic/reply_event.json. The Go half (core-go/internal/crmarena/synthetic_shape_test.go)
normalises that fixture next to a WP31-built base email and asserts no field's format differs.
Regenerate after an intended change: SYNTH_REGEN=1 python -m pytest tests/test_synthetic_render.py
"""
from __future__ import annotations

import json
import os
import re
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import leakcheck, records, render, seeding  # noqa: E402

SAMPLE = ROOT / "fixtures" / "crmarena_sample"
FIXTURE = ROOT / "fixtures" / "synthetic" / "reply_event.json"
REPS = ("techagents.com", "techdomain.com")


def _load(name: str) -> list[dict]:
    return json.loads((SAMPLE / f"{name}.json").read_text(encoding="utf-8"))


def _when(sf: str) -> datetime:
    return datetime.strptime(sf, "%Y-%m-%dT%H:%M:%S.%f%z").astimezone(timezone.utc)


def build_fixture() -> dict:
    contacts = {c["Email"].lower(): c for c in _load("Contact")}
    accounts = {a["Id"]: a["Name"] for a in _load("Account")}
    opps = {o["Id"]: o for o in _load("Opportunity")}
    out = next(e for e in sorted(_load("EmailMessage"), key=lambda e: e["Id"])
               if e["FromAddress"].split("@")[1] in REPS and e["ToAddress"].lower() in contacts and e["RelatedToId"] in opps)
    buyer = contacts[out["ToAddress"].lower()]
    reps = {u["Email"].lower(): u["Name"].strip() for u in _load("User") if u.get("Email")}  # = crmarena index names
    base = render.BaseEmail(out["Id"], out["RelatedToId"], out["Subject"], out["FromAddress"],
                            reps.get(out["FromAddress"].lower()), _when(out["MessageDate"]))
    ids = render.PROFILE["ids"]["02s"]
    email_id = records.sequential_id("EmailMessage", ids["max_body"], 0, ids["median_gap"], out["Id"])
    s = seeding.stream(seeding.DEFAULT_ROOT_SEED, "render", out["Id"])
    at = render.snap_time(base.at + timedelta(hours=1), base.at + timedelta(days=3), s)
    who = render.Buyer(buyer["Email"], f'{buyer["FirstName"]} {buyer["LastName"]}', accounts[buyer["AccountId"]],
                       opps[out["RelatedToId"]]["Name"])
    return render.render_reply(email_id, at, "positive", None, base, who, s)


def test_fixture_is_the_current_rendering() -> None:
    event = build_fixture()
    if os.environ.get("SYNTH_REGEN"):
        FIXTURE.parent.mkdir(parents=True, exist_ok=True)
        FIXTURE.write_text(json.dumps(event, indent=1) + "\n", encoding="utf-8")
    assert json.loads(FIXTURE.read_text(encoding="utf-8")) == event


def test_rendering_follows_the_wp31_scheme() -> None:
    e = build_fixture()
    p = e["payload"]
    assert re.fullmatch(r"02sWt[0-9A-Za-z]{13}", e["source_object_id"]) and p["message_id"] == e["source_object_id"]
    assert p["thread_id"] == p["crm_opportunity_ref"] and re.fullmatch(r"opp:006Wt[0-9A-Za-z]{13}", p["thread_id"])
    assert (e["connector"], e["connector_version"]) == ("crmarena-loader", "wp31-v1")
    assert re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:00Z", e["occurred_at"]) and p["date"] == e["occurred_at"]
    assert "cc" not in p and p["to"][0]["email"].endswith("@vendor.example")
    assert leakcheck.marker_leaks(e) == []


def test_synthetic_ids_continue_the_real_sequence() -> None:
    """Review MEDIUM: ids are sequential after the highest real id of the prefix, not uniform random."""
    ids = render.PROFILE["ids"]["02s"]
    seq = [records.sequential_id("EmailMessage", ids["max_body"], k, ids["median_gap"], f"s{k}") for k in range(50)]
    nums = [records.id_number(i) for i in seq]
    top = records.id_number("000" + ids["max_body"])
    assert all(n > top for n in nums) and nums == sorted(nums)
    assert max(nums) - top <= 50 * (ids["median_gap"] + 1) + ids["median_gap"]  # contiguous block
    assert all(records.sf_checksum(i[:15]) == i[15:] and i.startswith("02s" + ids["max_body"][:7]) for i in seq)
    hashed = [records.id_number(records.sf_id("EmailMessage", f"s{k}")) for k in range(50)]
    assert max(hashed) - min(hashed) > 1000 * (max(nums) - min(nums))  # the old uniform ids were spread out


def test_snapped_times_follow_the_real_business_hour_mix() -> None:
    s = seeding.stream(1, "snap")
    lo = datetime(2023, 3, 1, tzinfo=timezone.utc)
    times = [render.snap_time(lo, lo + timedelta(days=30), s) for _ in range(400)]
    hours = {t.strftime("%H") for t in times}
    assert hours <= set(render.PROFILE["email_hour_mix"]) and all(t.second == 0 for t in times)
    assert sum(t.strftime("%M") in ("00", "30") for t in times) / len(times) > 0.5
    narrow = render.snap_time(lo + timedelta(hours=10, minutes=1), lo + timedelta(hours=10, minutes=2), s)
    assert lo + timedelta(hours=10, minutes=1) < narrow <= lo + timedelta(hours=10, minutes=2)


def test_templates_are_in_the_length_range_of_real_customer_bodies() -> None:
    p10, _, p90 = render.PROFILE["customer_body_chars"]
    lengths = [len(t) for kind in render.TEMPLATES.values() if isinstance(kind, list) for t in kind]
    assert len(lengths) >= 12 and all(0.6 * p10 <= n <= 1.2 * p90 for n in lengths)
