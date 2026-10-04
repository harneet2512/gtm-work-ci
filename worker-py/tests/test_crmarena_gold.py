"""HAR-114 gold v2, part B: the CRMArena gold cases (55 cases built from real CRMArena-Pro B2B deals).

Per case: schema, catalog-consistent judgments, origin and licence. As a set: coverage of the 17 HAR-97 case types, both
should-pass values, the labelling passes, the manifest. The leakage tests and the generator tests are in
test_crmarena_leakage.py. Gold here are MODEL labels (one author, two passes), never human labels."""
from __future__ import annotations

import hashlib
import json
from collections import Counter
from pathlib import Path

import pytest

from crmarena_gold_lib import CR, CR_CASES, CR_FILES, CR_VALIDATOR, KNOWLEDGE_VALIDATOR, LABELS, OUTPUT_VALIDATOR, ROOT
from eval_cases_lib import CASES, CASE_TYPES, EVAL_TYPES
from test_contracts import load_json
from crmarena_gold import labels as L

ACTING = {"send_email", "schedule_meeting", "share_document", "internal_note"}
ACTION_LABELS = {"NO_ACTION_NEEDED": {"no_action"}, "NO_ACTION": {"no_action"}, "WAIT": {"wait"}, "ACT_NOW": ACTING}
MIN_CASES, MIN_PER_TYPE = 40, 3


@pytest.mark.parametrize("path", CR_FILES, ids=lambda p: p.stem)
def test_case_validates_against_schema_and_contracts(path: Path) -> None:
    case = load_json(path)
    errors = list(CR_VALIDATOR.iter_errors(case))
    assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]
    assert not list(OUTPUT_VALIDATOR.iter_errors(case["candidate_action"]))
    for k in case["context"]["offered_knowledge"]:
        assert not list(KNOWLEDGE_VALIDATOR.iter_errors(k)), k["key"]
    assert path.stem == case["id"] and path.parent.name == case["case_type"] and case["case_type"] in CASE_TYPES


@pytest.mark.parametrize("cid", list(CR_CASES))
def test_case_is_marked_crmarena_and_licensed(cid: str) -> None:
    case = CR_CASES[cid]
    assert case["origin"] == "crmarena" and case["slice_tags"]["origin"] == "crmarena" and case["based_on"]["kind"] == "crmarena_moment"
    assert case["source"]["licence"].startswith("CC BY-NC 4.0") and "Salesforce AI Research" in case["source"]["attribution"]
    assert case["labelling"]["label_kind"] == "model labels, single author, not human labels"


@pytest.mark.parametrize("cid", list(CR_CASES))
def test_expected_judgments_are_consistent(cid: str) -> None:
    case = CR_CASES[cid]
    exp = case["expected"]
    types = [e["eval_type"] for e in exp]
    assert len(types) == len(set(types)), "one judgment per eval type"
    assert any(e["verdict"] == "fail" for e in exp) != case["should_pass"], "should_pass <=> no expected fail"
    for e in exp:
        entry = EVAL_TYPES[e["eval_type"]]
        assert (e["label"] in entry["labels"]) if entry["labels"] else e["label"] is None, (e["eval_type"], e["label"])
        assert e["evidence_class"] == entry["evidence_class"]
        assert not e["blocking"] or (entry["can_block"] and e["verdict"] == "fail"), f"{e['eval_type']} cannot block here"
        if e["label"] in ACTION_LABELS:
            assert case["expected_best_action"]["action"] in ACTION_LABELS[e["label"]], (e["eval_type"], e["label"])
    offered = {k["id"] for k in case["context"]["offered_knowledge"]}
    assert set(case["candidate_action"].get("knowledge_refs_used", [])) <= offered
    best, cand = case["expected_best_action"], case["candidate_action"]
    if case["should_pass"]:
        assert best["action"] == cand["proposed_action_type"]
    for until in (cand.get("wait_until"), best.get("wait_until")):
        assert until is None or until > case["context"]["now"]
    tags = case["slice_tags"]
    assert tags["candidate"] == ("good" if case["should_pass"] else "flawed") and tags["expected_action"] == best["action"]
    if case["case_type"] in ("applicable_knowledge", "misleading_similar_knowledge"):
        assert offered and "knowledge_applicability" in types
    if case["case_type"] == "explicit_exception":
        assert offered and "exception_awareness" in types
    if case["case_type"] == "duplicate_action":
        assert "duplicate_action" in types


def test_every_case_type_is_covered_with_both_outcomes() -> None:
    assert len(CR_CASES) >= MIN_CASES and len(CR_CASES) == len(CR_FILES)
    counts = Counter(c["case_type"] for c in CR_CASES.values())
    assert {t: counts[t] for t in CASE_TYPES if counts[t] < MIN_PER_TYPE} == {}
    assert set(counts) == set(CASE_TYPES)
    flawed = Counter(c["case_type"] for c in CR_CASES.values() if not c["should_pass"])
    assert all(flawed[t] >= 1 for t in CASE_TYPES), "every case type needs a failing candidate"
    good = [c for c in CR_CASES.values() if c["should_pass"]]
    assert len(good) >= 12 and len(good) < len(CR_CASES), "false-block measurement needs good and bad candidates"
    assert {c["candidate_action"]["proposed_action_type"] for c in good} >= {"send_email", "schedule_meeting", "wait", "no_action"}


def test_legacy_no_action_coverage_is_restored_by_the_crmarena_gold() -> None:
    """Gold v2 turned the legacy should-pass no_action control into a flawed case; together the sets keep >= 3 and a good one."""
    best = Counter(c["expected_best_action"]["action"] for c in [*CASES.values(), *CR_CASES.values()])
    assert best["no_action"] >= 3 and best["wait"] >= 3
    assert any(c["should_pass"] and c["candidate_action"]["proposed_action_type"] == "no_action" for c in CR_CASES.values())


def test_valid_champion_handoff_cases_declare_their_evidence_gap() -> None:
    """CRMArena records no delegation or departure; the type is covered by analogues that say so."""
    handoff = [c for c in CR_CASES.values() if c["case_type"] == "valid_champion_handoff"]
    assert len(handoff) >= MIN_PER_TYPE and all("no delegation or departure" in c["evidence_gap"] for c in handoff)


def test_champion_is_always_inferred_never_recorded() -> None:
    for case in CR_CASES.values():
        group = case["context"]["state"]["buying_group"]
        champions = [m for m in group if "champion" in m["roles"]]
        assert len(champions) <= 1
        if champions:
            assert "inferred" in case["context"]["state_provenance"]["champion"]


def test_manifest_lists_every_case_with_its_hash() -> None:
    manifest = json.loads((CR / "manifest.json").read_text(encoding="utf-8"))
    on_disk = {p.relative_to(CR).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest() for p in CR_FILES}
    assert manifest["files"] == on_disk and manifest["cases"] == len(CR_FILES)
    combined = hashlib.sha256("".join(f"{k}:{v}\n" for k, v in sorted(on_disk.items())).encode("utf-8")).hexdigest()
    assert manifest["cases_sha256"] == combined
    assert "CC BY-NC 4.0" in manifest["licence"] and set(manifest["snapshot_file_sha256"]) >= {"EmailMessage", "Task", "Contact"}


def test_readme_carries_the_licence_and_attribution() -> None:
    text = (CR / "README.md").read_text(encoding="utf-8")
    assert "CC BY-NC 4.0" in text and "Salesforce AI Research" in text and "non-commercial" in text.lower()


# ---------- labelling passes ----------
def test_both_passes_cover_every_case_and_judgment() -> None:
    p1, p2 = L.load_pass("pass_1"), L.load_pass("pass_2")
    assert set(p1) == set(p2) == set(CR_CASES)
    for cid, case in CR_CASES.items():
        evals = [e["eval_type"] for e in case["expected"]]
        assert set(p1[cid]) == set(p2[cid]) == set(evals), cid
        for e in evals:
            assert not L.check_judgment(e, p1[cid][e]) and not L.check_judgment(e, p2[cid][e])
            assert len(p1[cid][e]) == 4 and len(p2[cid][e]) == 3, "pass 1 carries a rationale, pass 2 does not"


def test_every_disagreement_is_resolved_and_the_gold_follows_the_resolution() -> None:
    p1, p2, res = L.load_pass("pass_1"), L.load_pass("pass_2"), L.load_resolutions()
    diffs = {(c, e) for c, e, _, _ in L.disagreements(p1, p2)}
    assert {(c, e) for c, evals in res.items() for e in evals} == diffs, "resolutions cover exactly the disagreements"
    for cid, case in CR_CASES.items():
        by_type = {e["eval_type"]: e for e in case["expected"]}
        for eval_type, gold in by_type.items():
            want, reason = L.final_label(cid, eval_type, p1, p2, res)
            assert (gold["verdict"], gold["label"], gold["blocking"]) == tuple(want[:3]), (cid, eval_type)
            assert bool(reason) == bool(gold.get("debatable")), (cid, eval_type)
        recorded = {d["eval_type"] for d in case["labelling"]["disagreements"]}
        assert recorded == {e for (c, e) in diffs if c == cid}


def test_agreement_numbers_are_single_author_self_agreement() -> None:
    p1, p2 = L.load_pass("pass_1"), L.load_pass("pass_2")
    report = json.loads((ROOT / "bench" / "reports" / "crmarena-gold-agreement-2026-10-02.json").read_text(encoding="utf-8"))
    assert "NOT inter-rater and NOT human agreement" in report["label_kind"]
    for key, value in L.agreement(p1, p2).items():
        assert report[key] == value, key
    assert report["fail_kappa_between_passes"] == L.kappa(p1, p2)
    for case in CR_CASES.values():
        agree = case["labelling"]["agreement"]
        assert agree["judgments"] == len(case["expected"]) and agree["verdict_agree"] <= agree["judgments"]


def test_kappa_and_agreement_on_tiny_inputs() -> None:
    a = {"c": {"x": ["fail", None, True], "y": ["pass", None, False]}}
    same, flipped = {"c": {"x": ["fail", None, True], "y": ["pass", None, False]}}, {"c": {"x": ["pass", None, False], "y": ["pass", None, False]}}
    assert L.kappa(a, same) == 1.0 and L.agreement(a, same)["exact_agree"] == 2
    assert L.agreement(a, flipped)["fail_agree"] == 1 and L.disagreements(a, flipped) == [("c", "x", a["c"]["x"], flipped["c"]["x"])]
    assert L.check_judgment("grounding", ["pass", "APPLIES", False]) and L.check_judgment("rep_style", ["warn", None, True])


def test_labels_needed_lists_every_disagreement_and_every_inferred_champion_case() -> None:
    needed = json.loads((LABELS / "labels_needed.json").read_text(encoding="utf-8"))
    assert needed["label_kind"].startswith("model labels") and "HAR-97 L5" in needed["human_agreement"]
    listed = {(i["case"], i["eval_type"]) for i in needed["judgments"]}
    res = L.load_resolutions()
    assert {(c, e) for c, evals in res.items() for e in evals} <= listed
    assert set(needed["cases_with_inferred_champion"]) == {cid for cid, c in CR_CASES.items()
                                                           if any("champion" in m["roles"] for m in c["context"]["state"]["buying_group"])}
    assert set(needed["cases_with_evidence_gap"]) == {cid for cid, c in CR_CASES.items() if c.get("evidence_gap")}
