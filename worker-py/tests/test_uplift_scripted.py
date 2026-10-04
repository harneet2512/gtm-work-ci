"""bench/uplift/scripted: the scripted stand-in for the model used to rehearse the stack at zero live calls."""
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from bench.uplift import scripted as S  # noqa: E402

BUYER = "11111111-1111-4111-8111-111111111111"
OWNER = "22222222-2222-4222-8222-222222222222"
ACTIVITY = "33333333-3333-4333-8333-333333333333"
KNOWLEDGE = "44444444-4444-4444-8444-444444444444"


def transcript(guided: bool) -> list[dict]:
    user = "Agent Run ...\n"
    if guided:
        guidance = {"supporting_knowledge": [{"applies": True, "knowledge_id": KNOWLEDGE}]}
        user += "Decision Guidance (apply before deciding):\n" + json.dumps(guidance, sort_keys=True)
    calls = [{"id": "c1", "type": "function", "function": {"name": "people", "arguments": "{}"}},
             {"id": "c2", "type": "function", "function": {"name": "activities", "arguments": "{}"}},
             {"id": "c3", "type": "function", "function": {"name": "state", "arguments": "{}"}}]
    results = {"c1": {"tool": "people", "items": [{"person_id": BUYER}]},
               "c2": {"tool": "activities", "items": [{"activity_id": ACTIVITY}]},
               "c3": {"tool": "state", "items": [{"field": "owner", "value": OWNER}]}}
    return ([{"role": "user", "content": user}, {"role": "assistant", "content": None, "tool_calls": calls}]
            + [{"role": "tool", "tool_call_id": k, "content": json.dumps(v)} for k, v in results.items()])


def test_the_first_turn_pulls_people_activities_and_state() -> None:
    turn = S.ScriptedProvider().complete_turn(system="s", messages=[{"role": "user", "content": "u"}],
                                              tools=[{"type": "function"}], schema={}, schema_name="plan")
    assert [c.name for c in turn.tool_calls] == ["people", "activities", "state"] and turn.content is None


def test_without_guidance_the_scripted_agent_reprices_and_with_it_holds_and_cites_the_item() -> None:
    plain = S.ScriptedProvider().complete_turn(system="s", messages=transcript(False), tools=[], schema={}, schema_name="plan")
    guided = S.ScriptedProvider().complete_turn(system="s", messages=transcript(True), tools=[], schema={}, schema_name="plan")
    p, g = plain.content["strategies"], guided.content["strategies"]
    assert p[0]["strategy_type"] == "offer_price_concession" and p[0]["knowledge_refs_used"] == []
    assert g[0]["strategy_type"] == "reinforce_value_case" and g[0]["knowledge_refs_used"] == [KNOWLEDGE]
    assert [s["strategy_type"] for s in p[1:]] == [s["strategy_type"] for s in g[1:]]
    assert g[0]["to"][0]["person_id"] == BUYER and g[2]["to"][0]["person_id"] == OWNER
    assert g[0]["evidence_refs"][0]["activity_id"] == ACTIVITY


def test_the_drafter_writes_one_templated_artifact_per_strategy() -> None:
    plan = [{"strategy_type": "ask_internal_owner", "title": "Ask", "description": "Check", "action": "internal_note"},
            {"strategy_type": "offer", "title": "Offer", "description": "Do it", "action": "send_email"}]
    user = "Account x\nStrategies (best first):\n" + json.dumps(plan) + "\n\nPulled context"
    out = S.ScriptedProvider().complete_json(system="s", user=user, schema={}, schema_name="artifacts").content["artifacts"]
    assert [(a["strategy_type"], a["artifact"]["channel"]) for a in out] == [("ask_internal_owner", "slack"), ("offer", "email")]
    assert out[0]["artifact"]["subject"] is None and out[1]["artifact"]["subject"] == "Re: Offer"
