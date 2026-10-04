"""HAR-140 (WP36): the registry split. Every legacy id maps to a new one, the headline set is the canonical 11, the
operational diagnostics are a separate class, V5 and V6 exist, and no blocker text is stale against main."""
from __future__ import annotations

import re
from collections import Counter

import pytest

from test_contracts import CONTRACTS, load_json

ROOT = CONTRACTS.parent
REGISTRY = load_json(CONTRACTS / "metrics" / "metric_registry.json")
METRICS: list[dict] = REGISTRY["metrics"]
BY_ID = {m["id"]: m for m in METRICS}
LEGACY = load_json(CONTRACTS / "metrics" / "legacy_id_map.json")
EVAL_IDS = {e["id"] for e in load_json(CONTRACTS / "evals" / "eval_registry.json")["evals"]}

HEADLINE_NAMES = ["trace-supported decision quality", "material human intervention", "eval catch rate on human-visible failures",
                  "false-pass / false-block", "repeat semantic correction", "knowledge mutation precision",
                  "knowledge behavioral-realization", "knowledge uplift + negative transfer",
                  "correction propagation accuracy + stale-dependency survival", "end-to-end workflow success",
                  "downstream customer/business outcome signals"]
STALE = ("No semantic judge has run", "No evaluator has been run against the 87", "No TriggerEvaluation is produced on main",
         "Deterministic evals (Go) are HAR-115 (WP17), not on main", "No knowledge learner or applicability checker on main",
         "labelled replies are planned in HAR-131", "deal outcomes are planned from the CRMArena-Pro replay",
         "No eval router or judges on main", "The agent emits one decision, not a ranked candidate list",
         "nothing scores its material fields", "Also needs StateTransition (HAR-126)", "HAR-127", "PR #17")
PATH = re.compile(r"\b((?:bench|contracts|core-go|worker-py|fixtures)/[A-Za-z0-9_./-]+)")


def test_legacy_map_covers_all_268_old_entries_and_each_maps_to_a_new_entry() -> None:
    old = LEGACY["map"]
    assert LEGACY["legacy_registry"]["entries"] == 268 == len(old) == len({r["old"] for r in old})
    assert re.fullmatch(r"[0-9a-f]{64}", LEGACY["legacy_registry"]["sha256"])
    missing = [r["old"] for r in old if r["new"] not in BY_ID]
    assert not missing, f"legacy ids with no new entry: {missing}"
    assert len({r["new"] for r in old}) == 268, "no two legacy entries collapse into one"
    for r in old:
        assert BY_ID[r["new"]]["kind"] == r["kind"] and BY_ID[r["new"]]["layer"] == r["layer"], r["old"]


def test_nothing_was_lost_and_only_the_canonical_entries_were_added() -> None:
    legacy_ids = {r["new"] for r in LEGACY["map"]}
    added = [m for m in METRICS if m["id"] not in legacy_ids]
    assert len(METRICS) == 278 and len(added) == 10
    assert {m["kind"] for m in added} == {"canonical"} and all(m["id"].startswith("canon.") for m in added)


def test_the_legacy_combined_registry_is_gone_because_nothing_reads_it() -> None:
    assert not (CONTRACTS / "metrics" / "har97_metrics.json").exists()
    assert not (CONTRACTS / "schemas" / "metrics_registry.v1.json").exists()
    readers = [p for p in (ROOT / "bench" / "metrics").glob("*.py") if "har97_metrics" in p.read_text(encoding="utf-8")]
    assert not readers, readers


def test_status_counts_before_and_after_the_refresh() -> None:
    before = Counter(r["status"] for r in LEGACY["map"])
    after = Counter(BY_ID[r["new"]]["status"] for r in LEGACY["map"])
    assert before == {"blocked": 224, "measured": 30, "computable": 14}
    assert after == {"blocked": 107, "measured": 30, "computable": 131}
    assert Counter(m["status"] for m in METRICS) == {"computable": 132, "measured": 30, "blocked": 116}
    assert {r["new"] for r in LEGACY["map"] if r["status"] == "measured"} == {m["id"] for m in METRICS if m["status"] == "measured"}


def test_headline_set_is_the_canonical_11_replacing_p1_to_p7() -> None:
    hs = REGISTRY["headline_set"]
    assert [h["rank"] for h in hs] == list(range(1, 12)) and [h["name"] for h in hs] == HEADLINE_NAMES
    assert all(h["source"] == f"HAR-97 Canonical §9 item {h['rank']}" for h in hs)
    assert {h["replaces"] for h in hs} == {"P1", "P2", "P3", "P4", "P5", "P6", "P7", None}
    assert [h["rank"] for h in hs if h["replaces"] is None] == [6, 7, 9], "the three headline items the old P1-P7 lacked"
    for h in hs:
        assert set(h["metric_ids"]) <= set(BY_ID) and set(h["evals"]) <= EVAL_IDS, h["id"]
    assert not [m["id"] for m in METRICS if "headline" in m], "the single old `headline` flag is replaced by the set"
    assert "eval_quality.repeat_semantic_correction_rate" in hs[4]["metric_ids"]


def test_operational_diagnostics_m1_to_m5_are_a_separate_class() -> None:
    ops = REGISTRY["operational_diagnostics"]
    assert [o["eval"] for o in ops] == ["M1", "M2", "M3", "M4", "M5"] and {o["class"] for o in ops} == {"operational"}
    assert not {o["id"] for o in ops} & set(BY_ID), "operational diagnostics are not in the learning metric list"
    assert all(not o["knowledge_mutation_allowed"] and set(o["consumers"]) <= {"report", "dashboard", "budget_alert"} for o in ops)
    assert not [m["id"] for m in METRICS if {e for e in m["evals"] if e.startswith("M")}], "no learning metric aggregates M1-M5"
    measures = " ".join(" ".join(o["measures"]) for o in ops)
    for word in ("input tokens", "tool calls", "eval latency", "time to first decision", "cost / successful workflow"):
        assert word in measures


def test_v5_and_v6_are_registered_with_their_frontier_quotes() -> None:
    v5, v6 = BY_ID["canon.v5_eval_saturation_discrimination"], BY_ID["canon.v6_eval_gaming_proxy_failure"]
    assert v5["har97_ref"]["section"] == "Frontier V5" and "separates good from bad" in v5["har97_ref"]["quote"]
    assert v6["har97_ref"]["section"] == "Frontier V6" and "improve the measured score" in v6["har97_ref"]["quote"]
    assert v5["status"] == "computable" and v6["status"] == "blocked" and v6["tracked_by"] == ["HAR-121"]
    assert {"E22.1", "E19.12"} <= set(v6["evals"]) and {"E19.3", "E19.4"} <= set(v5["evals"])


def test_every_canonical_entry_cites_a_har97_section_and_links_registered_evals() -> None:
    for m in (x for x in METRICS if x["kind"] == "canonical"):
        ref = m["har97_ref"]
        assert ref["source"] == "HAR-97" and re.match(r"^(Canonical §|Frontier V)", ref["section"]), m["id"]
        assert m["evals"] and set(m["evals"]) <= EVAL_IDS, m["id"]


def test_every_eval_link_refers_to_a_registered_eval() -> None:
    assert not [(m["id"], e) for m in METRICS for e in m["evals"] if e not in EVAL_IDS]
    assert Counter(bool(m["evals"]) for m in METRICS if m["layer"] in ("deterministic_evals", "semantic_evals"))[False] == 0


def test_no_stale_blocker_text_survives_the_refresh() -> None:
    text = "\n".join((m.get("blocker") or "") + " " + m.get("notes", "") for m in METRICS)
    assert not [s for s in STALE if s in text]


def test_ticket_merged_on_main_are_not_described_as_missing() -> None:
    """HAR-115 (WP17), HAR-116 (WP18), HAR-118 (WP20), HAR-114 (WP16) and the WP8 signals are merged."""
    det = [m for m in METRICS if m["id"].startswith("det.")]
    assert len(det) == 30 and {m["status"] for m in det} == {"computable"}
    assert all("HAR-115" in m["notes"] and "not on main" not in m["notes"] for m in det)
    judged = BY_ID["l2.grounding"]
    assert judged["status"] == "computable" and "judges-2026-10-03" in judged["notes"]
    assert BY_ID["trigger.false_positive_rate"]["status"] == "computable"
    assert BY_ID["l1.material_change_detection"]["status"] == "computable"
    assert BY_ID["l4.applicability_precision"]["status"] == "computable"
    assert "decision_episode.v1" in BY_ID["human_feedback.manual_replacement_rate"]["blocker"]
    assert BY_ID["gate.agent_gets_state_change_evidence"]["status"] == "computable"


def test_blocker_and_note_paths_exist_in_the_repo() -> None:
    missing = []
    for m in METRICS:
        for text in (m.get("blocker") or "", m.get("notes", "")):
            for path in PATH.findall(text):
                path = path.rstrip(".,;:)")
                if not (ROOT / path).exists():
                    missing.append((m["id"], path))
    assert not missing, missing


@pytest.mark.parametrize("layer", ["edit_propagation"])
def test_new_layers_are_ordered_after_knowledge_l4(layer: str) -> None:
    order = REGISTRY["layer_order"]
    assert order.index(layer) == order.index("knowledge_L4") + 1
