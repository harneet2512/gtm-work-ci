"""A small hand-built A/B/C experiment (6 situations) shared by the uplift report, blind-pair and example tests.

Shape of the real run: one learned item survives the lifecycle (about the price pushback), the other promoted items were
disputed and are not retrievable, so the second-quote situations are controls (arms A and C) with the price item as the
irrelevant knowledge. Every candidate below is invented test data in the shape of the worker's strategy candidate;
nothing here is model output."""
from __future__ import annotations

import copy
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from bench.uplift.learn import det_uuid  # noqa: E402

K_PRICE = det_uuid("knowledge", "price_pushback")
K_QUOTE = det_uuid("knowledge", "second_quote")
K_VOLUME = det_uuid("knowledge", "high_outbound_volume")
CUTOFF = "2023-11-01T00:00:00Z"
SHA = "0" * 64

REPRICE = ("offer_price_concession", "Offer a price concession", "Reduce the quoted amount to keep the deal moving")
HOLD = ("reinforce_value_case", "Reinforce the value case", "Hold the quoted price and walk through the return on investment")
SEPARATE = ("send_separate_quote", "Send a separate quote", "Issue a new quote for the additional scope")
FOLD = ("fold_into_open_quote", "Consolidate into the open quote", "Update the existing quote rather than sending a new one")
NEITHER = ("schedule_call", "Schedule a call", "Align on timing with the buyer")


def candidate(sid: str, arm: str, move: tuple[str, str, str], to: str = "p-buyer") -> dict:
    strategy, title, description = move
    return {"candidate_id": f"{sid}-{arm}", "strategy_type": strategy, "title": title, "description": description,
            "action_type": "send_email", "ranking": 1, "preferred_by_agent": True,
            "to": [{"person_id": to, "role": "to", "why": "the buyer"}], "cc": [],
            "full_action_artifact": {"channel": "email", "subject": f"Re: {title}", "body": f"Hi, {description}. Best, Sam"}}


def arm(sid: str, name: str, move, *, retrieved=(), applicable=(), blocked=(), cited=(), corrections=()) -> dict:
    return {"candidate": candidate(sid, name, move), "corrections": list(corrections),
            "knowledge": {"retrieved": list(retrieved), "applicable": list(applicable),
                          "exception_blocked": list(blocked), "cited": list(cited)}}


# id, kind, decision point, A move, knowledge arm move (B on discriminating/exception, C on control), learned item of the point
SITUATIONS = [
    ("S-price-1", "discriminating", "price_pushback", REPRICE, HOLD, K_PRICE),
    ("S-price-2", "discriminating", "price_pushback", REPRICE, HOLD, K_PRICE),
    ("S-price-3", "discriminating", "price_pushback", HOLD, HOLD, K_PRICE),
    ("S-quote-c1", "control", "second_quote", SEPARATE, SEPARATE, K_PRICE),
    ("S-quote-c2", "control", "second_quote", FOLD, FOLD, K_PRICE),
    ("S-price-x", "exception", "price_pushback", NEITHER, NEITHER, K_PRICE),
]


def arms_doc() -> dict:
    sits = []
    for sid, kind, point, a_move, k_move, item in SITUATIONS:
        arms = {"A": arm(sid, "A", a_move, corrections=["evidence_grounding"] if sid == "S-price-1" else [])}
        if kind == "control":  # the price item is retrieved and does not apply to a second-quote decision
            arms["C"] = arm(sid, "C", k_move, retrieved=[item])
        else:
            disc = kind == "discriminating"
            arms["B"] = arm(sid, "B", k_move, retrieved=[item], applicable=[item] if disc else [],
                            cited=[item] if disc and k_move is HOLD else [])
        sits.append({"id": sid, "arms": arms})
    return {"version": "abc_arms.v1", "situations": sits}


def pack() -> dict:
    out = []
    for i, (sid, kind, point, *_rest) in enumerate(SITUATIONS):
        out.append({"id": sid, "kind": kind, "decision_point": point, "deal_id": f"006Wt00000FIXTURE{i}",
                    "trigger": {"source_object_id": f"02sWt0000000{i}", "source_event_key": "received",
                                "occurred_at": f"2023-11-{10 + i:02d}T15:00:00Z"},
                    "selection_reason": f"fixture reason {sid}"})
    return {"situations": out}


def learning_doc() -> dict:
    hyps = [{"decision_point": p, "kind": k, "n_option": 90, "n_other": 100, "win_rate_option": 0.33, "win_rate_other": 0.54,
             "p_value": pv, "promoted": pr}
            for p, k, pv, pr in (("ops_outreach", "action", 0.077, False), ("price_pushback", "action", 0.0025, True),
                                 ("technical_reply", "action", 0.247, False), ("second_quote", "action", 0.0258, True),
                                 ("first_send_early_week", "control", 0.93, False), ("high_outbound_volume", "control", 0.0128, True),
                                 ("any_colleague_cc", "control", 0.61, False))]
    knowledge = [{"id": K_PRICE, "decision_point": "price_pushback"}, {"id": K_QUOTE, "decision_point": "second_quote"},
                 {"id": K_VOLUME, "decision_point": "high_outbound_volume"}]
    return {"cutoff": CUTOFF, "previous_deals": 564, "promotion_rule": "fixture promotion rule", "hypotheses": hyps,
            "knowledge": knowledge}


def store() -> list[dict]:
    counts = {"decisions": 106, "positive_reactions": 2, "negative_reactions": 1, "outcomes_advanced": 58, "counterexamples": 0}
    disputed = {"decisions": 55, "positive_reactions": 1, "negative_reactions": 21, "outcomes_advanced": 29, "counterexamples": 0}
    history = [{"from_status": "candidate", "to_status": "disputed", "reason": "disputed: 3 negative reactions (>= 3) outnumber 0 positive",
                "changed_at": CUTOFF}]
    return [{"id": K_PRICE, "key": "K202", "title": "Hold the quoted amount after a price objection", "status": "provisional",
             "counts": dict(counts), "created_at": CUTOFF},
            {"id": K_QUOTE, "key": "K204", "title": "Fold a new quote into the one still open", "status": "disputed",
             "counts": dict(disputed), "created_at": CUTOFF, "status_history": history},
            {"id": K_VOLUME, "key": "K206", "title": "Send more emails than usual", "status": "disputed",
             "counts": dict(disputed), "created_at": CUTOFF, "status_history": history}]


def guard() -> dict:
    return {"empty_before_learning": True, "admitted": 3, "hidden_rule_text_found": 0, "seeded_or_manual_found": 0,
            "hard_block_passed": True}


def provenance() -> dict:
    return {"runtime_model": {"configured": "openrouter/qwen/qwen3.8-27b:free", "family": "qwen3.8-27b",
                              "answered": ["qwen/qwen3.8-27b:free"]},
            "llm_mode": "replay", "calls": {"generation_runs": 12, "recorded_llm_calls": 36}, "seed": 20261004,
            "cassettes": {"count": 36, "sha256": SHA},
            "data": {"synthetic_manifest_sha256": SHA, "base_export_sha256": SHA, "rules_version": "synthetic_rules:v1",
                     "rules_sha256": SHA, "split_sha256": SHA, "pack_sha256": SHA, "learning_sha256": SHA}}


def fresh() -> dict:
    return copy.deepcopy({"arms": arms_doc(), "pack": pack(), "learning": learning_doc(), "store": store(),
                          "guard": guard(), "provenance": provenance()})
