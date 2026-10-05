"""HAR-140 (WP36): the 20 x 10 completeness matrix and its construction gate (HAR-97 canonical sections 1 and 13)."""
from __future__ import annotations

import copy
import sys

import pytest

from test_contracts import CONTRACTS, example, load_json, validator

sys.path.insert(0, str(CONTRACTS.parent / "bench" / "metrics"))

import completeness_gate as gate  # noqa: E402

MATRIX = load_json(gate.MATRIX_PATH)
REGISTRY = load_json(gate.REGISTRY_PATH)
CELLS = {(c["surface"], c["dimension"]): c for c in MATRIX["cells"]}
SURFACES = ["source/evidence", "inference boundary", "entity/account resolution", "graph/world mutation",
            "state/transition interpretation", "precedent retrieval", "company-knowledge applicability",
            "knowledge influence on reasoning", "decision construction", "decision ranking/abstention",
            "human interaction interpretation", "edit/override propagation", "action/output generation",
            "tool use/permissions/writes", "execution/environment outcome", "knowledge mutation",
            "knowledge to future-behavior realization", "continual learning/regression/negative transfer",
            "trace integrity + grader reliability + adversarial robustness", "compute/token/latency/cost"]


def _mutated(mutate) -> list[str]:
    matrix, registry = copy.deepcopy(MATRIX), copy.deepcopy(REGISTRY)
    mutate(matrix, registry)
    return gate.check(matrix, registry)


def CELLS_COPY(matrix: dict, surface: int, dimension: str) -> dict:
    cell = next(c for c in matrix["cells"] if (c["surface"], c["dimension"]) == (surface, dimension))
    for key in ("reason",):
        cell.pop(key, None)
    return cell


def _first(matrix: dict, state: str) -> dict:
    return next(c for c in matrix["cells"] if c["state"] == state)


def test_matrix_and_example_validate() -> None:
    for doc in (MATRIX, example("completeness_matrix")):
        errors = list(validator("completeness_matrix").iter_errors(doc))
        assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]


def test_the_matrix_is_the_20_canonical_surfaces_by_10_dimensions() -> None:
    assert [s["name"] for s in MATRIX["surfaces"]] == SURFACES and [s["id"] for s in MATRIX["surfaces"]] == list(range(1, 21))
    assert len(MATRIX["dimensions"]) == 10 and len(MATRIX["cells"]) == 200 == len(CELLS)


def test_the_construction_gate_passes_on_the_committed_matrix() -> None:
    assert gate.check(MATRIX, REGISTRY) == []


def test_cell_counts_are_pinned_so_a_change_is_deliberate() -> None:
    c = gate.counts(MATRIX, REGISTRY)
    assert c == gate.Counts(cells=200, covered=69, covered_by_implemented=30, covered_partial_only=39, planned=103,
                            planned_with_registered_eval=103, not_applicable=28)
    assert c.covered + c.planned + c.not_applicable == 200


def test_a_required_cell_with_no_eval_and_no_plan_fails_the_gate() -> None:
    def drop_plan(matrix: dict, _: dict) -> None:
        cell = _first(matrix, "REQUIRED")
        cell.pop("status")
        cell.pop("owner")
    problems = _mutated(drop_plan)
    assert any("REQUIRED with no eval registered and not planned with an owning Linear issue" in p for p in problems)


def test_a_planned_cell_with_an_owner_but_no_registered_planned_eval_fails_the_gate() -> None:
    problems = _mutated(lambda matrix, _: _first(matrix, "REQUIRED").update(evals=[]))
    assert any("and a registered planned eval" in p for p in problems)


def test_an_owner_the_registries_do_not_know_fails_the_gate() -> None:
    problems = _mutated(lambda matrix, _: _first(matrix, "REQUIRED").update(owner="HAR-9999"))
    assert any("not a Linear issue the registries know" in p for p in problems)


def test_a_not_applicable_reason_must_cite_a_surface_or_an_eval() -> None:
    junk = _mutated(lambda matrix, _: _first(matrix, "NOT_APPLICABLE").update(reason="This is simply not something we need."))
    assert any("must cite a surface or an eval" in p for p in junk)
    ok = _mutated(lambda matrix, _: _first(matrix, "NOT_APPLICABLE").update(reason=_first(MATRIX, "NOT_APPLICABLE")["reason"] + " See E1.1."))
    assert ok == []


def test_a_covered_cell_whose_eval_no_longer_exists_fails_the_gate() -> None:
    def retire(_: dict, registry: dict) -> None:
        cell = _first(MATRIX, "COVERED_BY")
        for e in registry["evals"]:
            if e["id"] in cell["evals"]:
                e.update(status="planned", grader=None, mode=None, implemented_by=[])
    assert any("covered cells list only existing evals" in p for p in _mutated(retire))


@pytest.mark.parametrize(("why", "mutate", "needle"), [
    ("planned owner is a Linear issue", lambda m, r: _first(m, "REQUIRED").update(owner="WP36"), "not planned with an owning"),
    ("a covered cell may not be left planned", lambda m, r: CELLS_COPY(m, 14, "ADVERSARIAL_GAMING").update(state="REQUIRED", status="planned", owner="HAR-117", evals=["E13.12"]), "protect|credited|tagged|mark it COVERED_BY"),
    ("a plain REQUIRED is not a plan", lambda m, r: _first(m, "REQUIRED").update(status="open"), "not planned with an owning"),
    ("a covered cell names a missing eval", lambda m, r: _first(m, "COVERED_BY")["evals"].append("E99.1"), "is not in the registry"),
    ("a covered cell may not list a planned eval", lambda m, r: _first(m, "COVERED_BY")["evals"].append("E1.4"), "does not protect|planned"),
    ("not applicable needs a reason", lambda m, r: _first(m, "NOT_APPLICABLE").update(reason="n/a"), "needs a reason"),
    ("a cell is covered by an eval that protects another surface", lambda m, r: _first(m, "COVERED_BY").update(evals=["E12.4"]), "does not protect"),
    ("an existing eval must be credited", lambda m, r: _first(m, "COVERED_BY")["evals"].pop(), "COVERED_BY without evals|not credited"),
    ("a cell may not disappear", lambda m, r: m["cells"].pop(), "missing cell"),
    ("a cell may not repeat", lambda m, r: m["cells"].append(copy.deepcopy(m["cells"][0])), "duplicate cell"),
    ("unknown state", lambda m, r: _first(m, "REQUIRED").update(state="MAYBE"), "unknown state"),
])
def test_the_gate_rejects_inconsistent_cells(why: str, mutate, needle: str) -> None:
    problems = _mutated(mutate)
    assert problems and any(any(n in p for n in needle.split("|")) for p in problems), (why, problems)


def test_an_eval_tagged_to_a_not_applicable_cell_fails_the_gate() -> None:
    def tag(matrix: dict, registry: dict) -> None:
        cell = _first(matrix, "NOT_APPLICABLE")
        registry["evals"][0]["surfaces"] = sorted({*registry["evals"][0]["surfaces"], cell["surface"]})
        registry["evals"][0]["dimensions"] = sorted({*registry["evals"][0]["dimensions"], cell["dimension"]})
    assert any("not applicable but eval" in p for p in _mutated(tag))


def test_planned_cells_are_owned_by_issues_the_registries_already_name() -> None:
    owners = {c["owner"] for c in MATRIX["cells"] if c["state"] == "REQUIRED"}
    assert owners <= gate.known_tickets(), owners - gate.known_tickets()


def test_every_planned_cell_names_a_registered_planned_eval_that_protects_it() -> None:
    by_id = gate.entries_by_id(REGISTRY)
    for c in (x for x in MATRIX["cells"] if x["state"] == "REQUIRED"):
        assert c["evals"], c
        assert all(by_id[i]["status"] in ("planned", "hidden") for i in c["evals"]), c  # hidden: out of scope, still owned


def test_the_cells_the_review_named_are_classified_as_decided() -> None:
    assert CELLS[(1, "ADVERSARIAL_GAMING")]["state"] == "REQUIRED" and CELLS[(1, "ADVERSARIAL_GAMING")]["owner"] == "HAR-121"
    assert "E22.10" in CELLS[(1, "ADVERSARIAL_GAMING")]["evals"]
    assert CELLS[(14, "ADVERSARIAL_GAMING")]["state"] == "COVERED_BY" and {"E13.10", "E13.14"} <= set(CELLS[(14, "ADVERSARIAL_GAMING")]["evals"])
    assert CELLS[(15, "ADVERSARIAL_GAMING")]["state"] == "COVERED_BY" and "E14.4" in CELLS[(15, "ADVERSARIAL_GAMING")]["evals"]
    assert not [c for c in MATRIX["cells"] if c["state"] == "NOT_APPLICABLE" and "E22 names no gaming mode" in c["reason"]]


def test_planned_trace_cells_name_a_span_of_the_canonical_trace_that_covers_the_surface() -> None:
    spans = load_json(CONTRACTS / "traces" / "trace_schema.json")["spans"]
    covered = {s for sp in spans for s in sp["surfaces"]}
    traced = [c for c in MATRIX["cells"] if c["state"] == "REQUIRED" and "trace_schema.json" in c.get("note", "")]
    assert len(traced) == 20 and all(c["surface"] in covered for c in traced)


def test_the_gate_is_not_a_vacuous_pass_every_state_occurs() -> None:
    assert {c["state"] for c in MATRIX["cells"]} == {"COVERED_BY", "REQUIRED", "NOT_APPLICABLE"}
    assert gate.main() == 0
