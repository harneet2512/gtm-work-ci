"""WP32 (HAR-131) v1.1: the uplift experiment's knowledge store starts empty and admits only knowledge
learned from previous deals; seeded demo knowledge (K17, every contract/fixture example, PR #23's
lessons and knowledge cassettes) is excluded, by id AND by text."""
from __future__ import annotations

import copy
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import experiment as X  # noqa: E402

K17 = json.loads((ROOT / "contracts" / "examples" / "knowledge.example.json").read_text(encoding="utf-8"))
RUN = X.LearningRun(previous_episode_ids=frozenset({"0e9e0000-0000-4000-8000-0000000000e1", "0e9e0000-0000-4000-8000-0000000000e2"}),
                    human_decided_episode_ids=frozenset({"0e9e0000-0000-4000-8000-0000000000e2"}))


def learned(**over: object) -> dict:
    item = copy.deepcopy(K17)
    item.update(id="11111111-0000-4000-8000-000000000001", key="K901", title="Learned pattern one",
                guidance={"summary": "Learned from previous deals.", "do": ["follow the learned pattern"], "dont": []},
                provenance={"created_from": "manual", "source_decision_episode_id": None},
                supporting_decision_episode_ids=["0e9e0000-0000-4000-8000-0000000000e1"])
    item["provenance"]["created_from"] = "human_delta"
    item["provenance"]["source_decision_episode_id"] = "0e9e0000-0000-4000-8000-000000000a02"
    item.update(over)
    return item


HUMAN_RUN = X.LearningRun(previous_episode_ids=RUN.previous_episode_ids,
                          human_decided_episode_ids=frozenset({"0e9e0000-0000-4000-8000-000000000a02"}))


def test_seeded_knowledge_includes_k17_and_every_fixture_item() -> None:
    seeded = X.seeded_knowledge()
    assert {"K17", K17["id"], "K05", "K08", "K09", "K21"} <= seeded.ids
    assert any("champion" in t for t in seeded.texts)


def test_store_must_be_empty_before_previous_deal_learning() -> None:
    X.assert_empty_before_learning([])
    with pytest.raises(X.ExperimentLeak):
        X.assert_empty_before_learning([K17])


def test_seeded_k17_is_never_admitted_even_with_previous_episodes() -> None:
    k17 = copy.deepcopy(K17)
    k17["supporting_decision_episode_ids"] = ["0e9e0000-0000-4000-8000-0000000000e1"]
    with pytest.raises(X.ExperimentLeak, match="seeded"):
        X.assert_experiment_store([k17], HUMAN_RUN)


def test_a_renamed_copy_of_seeded_wording_is_refused() -> None:
    copy_item = learned(title=K17["title"] + " (learned)")
    with pytest.raises(X.ExperimentLeak, match="seeded wording"):
        X.assert_experiment_store([copy_item], HUMAN_RUN)


@pytest.mark.parametrize(("over", "why"), [
    ({"provenance": {"created_from": "seed_history", "source_decision_episode_id": None}}, "not learned"),
    ({"provenance": {"created_from": "manual", "source_decision_episode_id": None}}, "not learned"),
    ({"supporting_decision_episode_ids": ["0e9e0000-0000-4000-8000-0000000000c9"]}, "outside the previous deals"),
    ({"status": "gospel"}, "knowledge.v1.json"),
])
def test_only_valid_knowledge_learned_from_previous_deals_is_admitted(over: dict, why: str) -> None:
    with pytest.raises(X.ExperimentLeak, match=why):
        X.assert_experiment_store([learned(**over)], HUMAN_RUN)


def test_human_delta_needs_a_real_human_decision_record() -> None:
    with pytest.raises(X.ExperimentLeak, match="human decision"):
        X.assert_experiment_store([learned()], RUN)


def test_knowledge_learned_from_previous_deals_is_admitted() -> None:
    X.assert_experiment_store([learned(), learned(id="11111111-0000-4000-8000-000000000002", key="K902",
                                                  title="Learned pattern two",
                                                  supporting_decision_episode_ids=["0e9e0000-0000-4000-8000-0000000000e1", "0e9e0000-0000-4000-8000-0000000000e2"])], HUMAN_RUN)


def test_pr23_lessons_and_knowledge_cassettes_are_on_the_denylist(tmp_path: Path) -> None:
    (tmp_path / "worker-py" / "tests").mkdir(parents=True)
    (tmp_path / "worker-py" / "cassettes" / "knowledge").mkdir(parents=True)
    (tmp_path / "worker-py" / "tests" / "knowledge_lessons.py").write_text(
        'L = ReferenceLesson(principle="When ownership is still unresolved during a possible expansion transition '
        'after a reorg, rebuild the relationship before making an expansion ask.")', encoding="utf-8")
    (tmp_path / "worker-py" / "cassettes" / "knowledge" / "k.json").write_text(
        json.dumps({"response": {"principle": "Keep the champion in the loop whenever a new security reviewer joins the deal."}}),
        encoding="utf-8")
    seeded = X.seeded_knowledge(tmp_path)
    assert any("rebuild the relationship before making an expansion ask" in t for t in seeded.texts)
    assert any("new security reviewer joins the deal" in t for t in seeded.texts)
    item = learned(title="Rebuild the relationship before making an expansion ask")
    with pytest.raises(X.ExperimentLeak, match="seeded wording"):
        X.admit(item, HUMAN_RUN, seeded)
