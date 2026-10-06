"""HAR-132 (WP33), HAR-140 (WP36): contracts/metrics/metric_registry.json is valid, has unique ids and is complete against HAR-97.

Completeness: every line HAR-97 marks "✅ acknowledged (Claude Code · HAR-n)" (snapshot:
contracts/metrics/har97_acknowledged_lines.txt) maps to a registry entry with the same owner whose har97_ref quotes it
under the same section; and no HAR-97 quote in the registry points at an unmarked line. `derived` entries are not
HAR-97 metrics (premature promotion from the §11(transition) acceptance list, the 15 HAR-129 integration gates)."""
from __future__ import annotations

import copy
import hashlib
import json
import re
from collections import Counter
from typing import NamedTuple

import pytest

from test_contracts import CONTRACTS, load_json, validator

ROOT = CONTRACTS.parent
REGISTRY = load_json(CONTRACTS / "metrics" / "metric_registry.json")
METRICS: list[dict] = REGISTRY["metrics"]
BY_ID = {m["id"]: m for m in METRICS}
ACK_FILE = CONTRACTS / "metrics" / "har97_acknowledged_lines.txt"
HAR97_KINDS = {"named_metric", "eval_check", "supervision_signal", "benchmark_case_type"}
PREFIX = re.compile(r"^(?:#+ |\* |> |→ )?(?:\d+(?:\.\d+)+ )?")


class Ack(NamedTuple):
    line: int
    section: str
    owner: str
    text: str


def normalize(text: str) -> str:
    """Drop the markdown prefix (heading, bullet, quote, arrow) and a heading number; compare case-insensitively."""
    return PREFIX.sub("", text.strip()).strip().casefold()


def load_acks() -> list[Ack]:
    rows = []
    for raw in ACK_FILE.read_text(encoding="utf-8").splitlines():
        if raw and not raw.startswith("#"):
            line, section, owner, text = raw.split("\t")
            rows.append(Ack(int(line), section, owner, text))
    return rows


ACKS = load_acks()
ACK_KEYS = {(a.section, normalize(a.text)): a.owner for a in ACKS}


def refs(metric: dict) -> list[dict]:
    ref = metric["har97_ref"]
    return [ref, *ref.get("also", [])]


def owner_ticket(metric: dict) -> str:
    return metric["owner_wp"].split(" ")[0]


def test_registry_validates_against_its_schema() -> None:
    errors = list(validator("metric_registry").iter_errors(REGISTRY))
    assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]


@pytest.mark.parametrize(("why", "mutate"), [
    ("measured needs a current value", lambda m: m.update(status="measured", current_value=None, blocker=None)),
    ("blocked needs a blocker", lambda m: m.update(status="blocked", blocker=None, current_value=None)),
    ("blocked has no value", lambda m: m.update(status="blocked", blocker="x")),
    ("unknown kind", lambda m: m.update(kind="metric")),
    ("unknown layer", lambda m: m.update(layer="L6")),
    ("owner must be a Linear WP", lambda m: m.update(owner_wp="WP33")),
    ("formula needs a ratio or a procedure", lambda m: m.update(formula={"numerator": "x"})),
    ("measured value needs a data_basis", lambda m: m["current_value"].pop("data_basis")),
    ("source must be a repo path", lambda m: m["current_value"].update(source="C:/tmp/x.json")),
    ("blocked needs tracked_by", lambda m: m.update(status="blocked", blocker="x", current_value=None)),
])
def test_schema_rejects_inconsistent_entries(why: str, mutate) -> None:
    doc = copy.deepcopy(REGISTRY)
    measured = next(m for m in doc["metrics"] if m["status"] == "measured")
    mutate(measured)
    assert list(validator("metric_registry").iter_errors(doc)), why


def test_ids_are_unique_and_parents_exist() -> None:
    assert not [i for i, n in Counter(m["id"] for m in METRICS).items() if n > 1]
    for m in METRICS:
        if "parent" in m:
            parent = BY_ID[m["parent"]]
            assert m["id"].startswith(parent["id"] + ".") and m["layer"] == parent["layer"], m["id"]
            assert m["owner_wp"] == parent["owner_wp"], m["id"]


def test_ack_snapshot_names_the_har97_version_and_has_304_unique_lines() -> None:
    assert len(ACKS) == 304
    assert len({a.line for a in ACKS}) == 304 and len(ACK_KEYS) == 304, "(section, text) identifies each line"
    assert "2026-10-02T19:33:49.030Z" in ACK_FILE.read_text(encoding="utf-8").splitlines()[1]
    assert REGISTRY["source"]["updated_at"] == "2026-10-02T19:33:49.030Z"
    assert hashlib.sha256(ACK_FILE.read_bytes()).hexdigest() == REGISTRY["source"]["acknowledged_lines_sha256"]
    assert REGISTRY["source"]["acknowledged_lines"] == ACK_FILE.relative_to(ROOT).as_posix()
    assert all(re.fullmatch(r"HAR-\d+", a.owner) for a in ACKS)


@pytest.mark.parametrize("ack", ACKS, ids=[f"{a.line}:{a.section}|{a.text[:40]}" for a in ACKS])
def test_every_acknowledged_line_maps_to_a_registry_id_with_its_owner(ack: Ack) -> None:
    hits = [m["id"] for m in METRICS if m["kind"] in HAR97_KINDS and any(
        r["section"] == ack.section and normalize(r["quote"]) == normalize(ack.text) for r in refs(m))]
    assert hits, f"HAR-97 line {ack.line} ({ack.section}) is not in the registry: {ack.text!r}"
    owners = {owner_ticket(BY_ID[i]) for i in hits}
    assert owners == {ack.owner}, f"line {ack.line}: owners {owners} != {ack.owner}"


def test_no_registry_quote_points_at_an_unmarked_har97_line() -> None:
    """Examples, label vocabularies, diagnostic labels, flows and guiding questions are not metrics."""
    for m in METRICS:
        if m["kind"] not in HAR97_KINDS:
            continue
        for r in refs(m):
            key = (r["section"], normalize(r["quote"]))
            assert r["source"] == "HAR-97" and key in ACK_KEYS, (m["id"], r)
            assert ACK_KEYS[key] == owner_ticket(m), (m["id"], r, ACK_KEYS[key])


LEGACY_SOURCES = ("bench/reports/go-acceptance-", "fixtures/evals/", "bench/reports/live-")


def test_every_measured_value_states_its_data_basis() -> None:
    measured = [m for m in METRICS if m["status"] == "measured"]
    for m in measured:
        cv = m["current_value"]
        assert cv["data_basis"], m["id"]
        if cv["source"].startswith(LEGACY_SOURCES):
            assert cv["data_basis"] in ("legacy_fixture_world", "live_model_on_legacy_situations"), m["id"]
            assert "fixture" in cv["caveat"] and "invented" in cv["caveat"], m["id"]
    live = {m["id"] for m in measured if m["current_value"]["data_basis"] == "live_model_on_legacy_situations"}
    assert live == {"l1.critical_context_recall", "l3.correct_recipients", "abstention.correctness",
                    "online_product.workflow_completion"}
    assert "extract-v4" in BY_ID["l1.critical_context_recall"]["current_value"]["caveat"]


def test_every_computable_or_blocked_entry_is_tracked_by_a_linear_issue() -> None:
    for m in METRICS:
        if m["status"] != "measured":
            assert m["tracked_by"], m["id"]
    assert BY_ID["l3.no_unnecessary_tool_calls"]["tracked_by"] == ["HAR-117"]
    assert all(BY_ID[f"slices.{k}"]["tracked_by"] == ["HAR-121"] for k in ("motion", "stage", "champion"))


TRANSITION_MEASURED = ("l1.relationship_state_accuracy", "l1.transition_support", "l1.uncertainty_marking", "l1.premature_promotion",
)


def test_transition_entries_are_measured_on_the_spec_derived_gold_and_say_what_is_still_missing() -> None:
    """HAR-126: measured by `ghostctl transitions-eval` on fixtures/gold/transitions. That gold is spec-derived, so each
    entry says so and still points at the independent gold (HAR-130 CRMArena deals + HAR-131 synthetic layer) as pending."""
    text = json.dumps(REGISTRY, ensure_ascii=False)
    assert "HAR-127" not in text and "PR #17" not in text
    for i in TRANSITION_MEASURED:
        m = BY_ID[i]
        assert m["status"] == "measured" and m["blocker"] is None, i
        cv = m["current_value"]
        assert cv["data_basis"] == "spec_derived_transition_gold", i
        assert cv["source"] == "bench/reports/transitions-gold-2026-10-03.json", i
        assert "spec-derived" in cv["caveat"] and "HAR-130" in cv["caveat"] and "HAR-131" in cv["caveat"], i
    for i in ("l1.relationship_state_accuracy", "l1.transition_support", "l1.uncertainty_marking", "l1.premature_promotion"):
        assert any("fixtures/gold/transitions" in g for g in BY_ID[i]["inputs"]["gold"]), i


def test_review_wording_for_gates_contract_gap_and_diff_inputs() -> None:
    gates = [m for m in METRICS if m["layer"] == "integration_gate"]
    assert all("realistic account activity (HAR-129)" in m["definition"] for m in gates)
    trace = BY_ID["gate.trace_raw_activity"]["current_value"]
    assert "legacy fixture world only" in trace["display"] and trace["data_basis"] == "legacy_fixture_world"
    assert BY_ID["human_feedback.manual_replacement_rate"]["blocker"].startswith(
        "human_decision.v1 lacks MANUAL_REPLACEMENT")
    data = BY_ID["gate.show_what_evals_missed"]["inputs"]["data"]
    assert "DecisionEpisode.literal_diff" in data and "DecisionEpisode.semantic_diff" in data
    assert not [m["id"] for m in METRICS if "diff_categories" in m["id"]]
    assert "only measurable acceptance item" in BY_ID["l1.premature_promotion"]["notes"]


WP33_FILES = ["contracts/metrics/metric_registry.json", "contracts/metrics/legacy_id_map.json",
              "contracts/metrics/har97_acknowledged_lines.txt", "contracts/schemas/metric_registry.v1.json",
              "contracts/examples/metric_registry.example.json", "docs/metrics.md", "docs/metrics-learning-outcomes.md",
              "docs/eval-completeness.md", "docs/traceability/wp33.md", "docs/traceability/wp36.md",
              "bench/reports/metrics-2026-10-03.json", "bench/reports/metrics-2026-10-03.md",
              "bench/reports/go-acceptance-2026-10-02.txt", "contracts/evals/eval_registry.json",
              "contracts/evals/completeness_matrix.json", "contracts/traces/trace_schema.json",
              "contracts/schemas/eval_registry.v1.json", "contracts/schemas/completeness_matrix.v1.json",
              "contracts/schemas/trace_schema.v1.json", "core-go/internal/contracts/registry_split_test.go",
              "core-go/internal/contracts/operational_isolation_test.go"]


def test_wp33_files_including_generated_ones_stay_under_400_lines() -> None:
    paths = [ROOT / p for p in WP33_FILES] + sorted((ROOT / "bench" / "metrics").glob("*.py"))
    paths += sorted((ROOT / "worker-py" / "tests").glob("test_metrics_*.py"))
    long = {p.name: n for p in paths if (n := len(p.read_text(encoding="utf-8").splitlines())) >= 400}
    assert not long


def test_derived_entries_are_premature_promotion_and_the_15_har129_gates() -> None:
    derived = [m for m in METRICS if m["kind"] == "derived"]
    gates = [m for m in derived if m["har97_ref"]["source"] == "HAR-129"]
    assert len(gates) == 15 and all(m["layer"] == "integration_gate" and m["unit"] == "boolean" for m in gates)
    assert [m["id"] for m in derived if m not in gates] == ["l1.premature_promotion"]
    assert "does not prematurely promote" in BY_ID["l1.premature_promotion"]["har97_ref"]["quote"]


REQUIRED = {
    "state_context_L1": ["l1.entity_attachment", "l1.material_change_detection", "l1.state_field_accuracy",
                         "l1.transition_support", "l1.uncertainty_marking", "l1.premature_promotion",
                         "l1.contradiction_preservation", "l1.provenance_coverage", "l1.stale_fact_use"],
    "decision_L2": [f"l2.{d}" for d in ("buyer_readiness", "next_step_quality", "stakeholder_coverage",
                                        "economic_buyer_coverage", "champion_strength", "decision_process",
                                        "business_case", "momentum", "action_stage_fit", "expansion_readiness",
                                        "customer_risk_sensitivity", "grounding")],
    "trajectory_L3": [f"l3.{c}" for c in ("correct_trigger", "correct_skill_tool", "correct_arguments",
                                          "correct_recipients", "correct_approval_point", "no_blocked_action_executed",
                                          "no_stale_crm_write", "no_unnecessary_tool_calls", "dry_run_non_writing")],
    "trigger": ["trigger.false_positive_rate", "trigger.false_negative_rate"],
    "decision_ranking": ["decision_ranking.best_move_ranked_first"],
    "abstention": ["abstention.correctness"],
    "human_feedback": ["human_feedback.approve_unchanged_rate", "human_feedback.edit_rate",
                       "human_feedback.reject_rate", "human_feedback.ignore_rate"],
    "knowledge_L4": ["l4.extraction_fidelity", "l4.scope_exception_quality", "l4.applicability_precision",
                     "l4.applicability_recall", "l4.exception_recall", "l4.uplift_b_over_a", "l4.uplift_b_over_c",
                     "l4.uplift_a_parity_c", "l4.reduction_in_human_edits", "l4.decision_eval_pass_rate_gain",
                     "l4.repeated_failure_reduction", "l4.selected_action_change", "l4.exception_correctness",
                     "l4.negative_transfer_rate"],
    "meta_L5": [f"l5.agreement_{e}" for e in ("readiness", "next_step_quality", "stakeholder_coverage",
                                              "champion_handling", "economic_buyer_coverage", "decision_process",
                                              "business_case", "expansion_readiness", "knowledge_applicability",
                                              "knowledge_extraction")],
}


@pytest.mark.parametrize("layer", sorted(REQUIRED))
def test_required_metrics_exist_in_their_layer(layer: str) -> None:
    assert not [i for i in REQUIRED[layer] if i not in BY_ID]
    assert {BY_ID[i]["layer"] for i in REQUIRED[layer]} == {layer}


def test_layers_that_mirror_a_har97_list_have_its_full_length() -> None:
    count = Counter(m["layer"] for m in METRICS)
    expected = {"customer_reaction": 10, "business_outcome": 8, "online_product": 8, "slices": 12,
                "eval_routing": 16, "deterministic_evals": 7 + 23, "semantic_evals": 16 + 5, "benchmark_cases": 17}
    assert {k: count[k] for k in expected} == expected
    assert all(m["unit"] == "dimension" for m in METRICS if m["layer"] == "slices")
    subs = Counter(m["parent"] for m in METRICS if m["layer"] == "decision_L2" and "parent" in m)
    assert sum(subs.values()) == 39 and len(subs) == 7, "§15(GTM) Check/Evaluate/Combine criteria"


def test_section_20_and_22_metric_lists_are_complete() -> None:
    def named(section: str) -> list[str]:
        return [m["id"] for m in METRICS if m["har97_ref"]["section"] == section and m["kind"] == "named_metric"]
    assert len(named("§20")) == 6 and len(named("§22")) == 10
    assert not [m["id"] for m in METRICS if "headline" in m], "the headline set is REGISTRY['headline_set'] (HAR-97 canonical §9)"


def test_kinds_follow_the_har97_list_each_metric_comes_from() -> None:
    by_prefix: dict[str, set[str]] = {}
    for m in METRICS:
        by_prefix.setdefault(m["id"].split(".")[0], set()).add(m["kind"])
    assert by_prefix["eval_quality"] == by_prefix["online_product"] == by_prefix["slices"] == {"named_metric"}
    for prefix in ("l2", "l3", "trigger", "abstention", "decision_ranking", "det", "sem", "routing"):
        assert by_prefix[prefix] == {"eval_check"}, prefix
    for prefix in ("human_feedback", "customer_reaction", "business_outcome"):
        assert by_prefix[prefix] == {"supervision_signal"}, prefix
    assert by_prefix["bench"] == {"benchmark_case_type"} and by_prefix["gate"] == {"derived"}
    assert by_prefix["canon"] == {"canonical"}
    assert all(BY_ID[i]["kind"] == "named_metric" for i in REQUIRED["meta_L5"])


def test_owners_of_spot_checked_groups() -> None:
    assert {m["owner_wp"] for m in METRICS if m["id"].startswith("det.")} == {"HAR-115 (WP17)"}
    assert {m["owner_wp"] for m in METRICS if m["id"].startswith(("sem.", "l2."))} == {"HAR-116 (WP18)"}
    assert {m["owner_wp"] for m in METRICS if m["id"].startswith(("routing.", "l1."))} == {"HAR-126 (WP28)"}
    assert {m["owner_wp"] for m in METRICS if m["id"].startswith("bench.")} == {"HAR-114 (WP16)"}
    assert BY_ID["online_product.workflow_completion"]["owner_wp"] == "HAR-120 (WP22)"


def test_measured_metrics_cite_a_source_that_exists_in_the_repo() -> None:
    measured = [m for m in METRICS if m["status"] == "measured"]
    assert len(measured) >= 8 + 17
    for m in measured:
        assert (ROOT / m["current_value"]["source"]).exists(), m["id"]


def test_eval_types_refer_to_the_eval_catalog() -> None:
    catalog = load_json(CONTRACTS / "evals" / "eval_catalog.json")["eval_types"]
    for m in METRICS:
        assert set(m.get("eval_types", [])) <= set(catalog), m["id"]
    covered = {t for m in METRICS if m["layer"] in ("deterministic_evals", "semantic_evals", "decision_L2", "state_context_L1")
               for t in m.get("eval_types", [])}
    in_scope = {t for t, e in catalog.items() if e["kind"] in ("deterministic", "semantic") and "status" not in e}  # a planned type has no judge yet
    assert covered == in_scope, "every §4 / §5 / §15(GTM) catalog eval has a registry entry"


def test_layer_order_covers_every_layer_from_process_to_outcome() -> None:
    order = REGISTRY["layer_order"]
    assert set(order) == {m["layer"] for m in METRICS}
    assert order.index("trigger") < order.index("state_context_L1") < order.index("decision_L2")
    assert order.index("trajectory_L3") < order.index("human_feedback") < order.index("business_outcome")
