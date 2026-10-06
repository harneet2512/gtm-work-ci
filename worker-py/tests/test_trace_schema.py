"""HAR-140 (WP36): contracts/traces/trace_schema.json is the canonical DecisionEpisode trace (HAR-97 canonical section 3)."""
from __future__ import annotations

from test_contracts import CONTRACTS, example, load_json, validator

TRACE = load_json(CONTRACTS / "traces" / "trace_schema.json")
SPANS = TRACE["spans"]
BY_ID = {s["id"]: s for s in SPANS}

# HAR-97 canonical section 3, the chain a consequential episode must reconstruct (22 links).
CHAIN = ["original source/event IDs", "extracted evidence + explicit/inferred boundary", "entity/account resolution",
         "graph mutations", "state/transition version", "retrieved precedents", "retrieved company knowledge",
         "applicability judgments", "knowledge actually used", "candidate decisions", "ranking / uncertainty / abstention",
         "recommended decision", "human interaction", "final intended action", "generated artifact", "tool calls / writes",
         "actual executed action", "customer/world response", "later outcome", "proposed knowledge mutation",
         "resulting knowledge version", "future episodes influenced by that knowledge"]


def test_trace_schema_and_example_validate() -> None:
    for doc in (TRACE, example("trace_schema")):
        errors = list(validator("trace_schema").iter_errors(doc))
        assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]


def test_spans_are_exactly_the_section_3_chain_in_order() -> None:
    assert [s["label"] for s in SPANS] == CHAIN
    assert [s["order"] for s in SPANS] == list(range(1, 23)) and len(BY_ID) == 22


def test_every_span_derives_from_earlier_spans_so_the_trace_is_acyclic() -> None:
    for s in SPANS:
        for p in s["parents"]:
            assert BY_ID[p]["order"] < s["order"], (s["id"], p)
    assert [s["id"] for s in SPANS if not s["parents"]] == ["source_events"]


def test_an_action_traverses_backward_to_the_source_and_forward_to_learning() -> None:
    def ancestors(span_id: str) -> set[str]:
        out: set[str] = set()
        for p in BY_ID[span_id]["parents"]:
            out |= {p} | ancestors(p)
        return out
    back = ancestors(TRACE["traversal"]["backward_from"])
    assert "source_events" in back and {"evidence", "state_transition", "recommendation", "human_interaction"} <= back
    children = {s["id"]: {c["id"] for c in SPANS if s["id"] in c["parents"]} for s in SPANS}

    def descendants(span_id: str) -> set[str]:
        return {c for ch in children[span_id] for c in {ch} | descendants(ch)}
    assert TRACE["traversal"]["forward_from"] == "knowledge_version"
    assert descendants("executed_action") >= {"knowledge_mutation_proposal", "knowledge_version", "influenced_future_episodes"}


def test_the_spans_cover_every_behavioral_surface_and_every_span_carries_the_common_fields() -> None:
    covered = {s for sp in SPANS for s in sp["surfaces"]}
    assert covered == set(range(1, 19)), "surfaces 19 (validation) and 20 (operational) have no trace span"
    assert {"episode_id", "span_id", "parent_span_ids"} <= set(TRACE["common_fields"])
    assert {"eval_id", "span_id", "supporting_span_ids"} <= set(TRACE["eval_result_link"]["required_fields"])


def test_the_internal_trace_viewer_lists_the_section_11_nodes() -> None:
    assert len(TRACE["viewer_tree"]) == 18 and "evals by surface" in TRACE["viewer_tree"]
    assert "invalidated/recomputed spans after an edit" in TRACE["viewer_tree"]
