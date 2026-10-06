"""HAR-114 gold v2, part A: the adjudicated corrections to the legacy gold (bench/labels/adjudication-2026-10-02.json).

Two blind LLM labellers (A, B), the legacy gold and a blind tie-breaker C decided 38 disputed judgments: the fail/non-fail
majority of {A, B, gold, C}, C on a 2-2 tie. 14 are overturned by the rule. Two knowledge_applicability judgments keep the gold
(C answered 'does K17 apply?'); one more was rejected after review because both fail votes rest on false premises; 11 are
applied. These are model labels, never human labels; blocking stays catalog-rule only. The gold may differ from the base only
by what the record lists: a test reverses those edits and compares the result with the base hash."""
from __future__ import annotations

import copy
import hashlib
import importlib.util
import json

import pytest

from eval_cases_lib import CASES, EVAL_TYPES
from test_contracts import load_json
from test_fixtures import FIXTURES

ROOT = FIXTURES.parent
RECORD = load_json(ROOT / "bench" / "labels" / "adjudication-2026-10-02.json")
SOURCE = "adjudicated: 2 LLM labellers + GPT-5.6 tie-break, 2026-10-02"
KEPT = {("acme_cp4_k17_offered_but_ignored", "knowledge_applicability"), ("beta_cp3_ravi_only_thread", "knowledge_applicability")}


def non_fail(verdict: str) -> str:
    return "fail" if verdict == "fail" else "nonfail"


def majority(votes: dict) -> str:
    fails = [non_fail(votes[k]) for k in ("A_opus", "B_sonnet", "gold", "C_gpt56")].count("fail")
    return "fail" if fails > 2 else "nonfail" if fails < 2 else non_fail(votes["C_gpt56"])


def canon(obj: dict) -> str:
    return hashlib.sha256(json.dumps(obj, sort_keys=True, ensure_ascii=False).encode("utf-8")).hexdigest()


def reverse_edits(case: dict) -> dict:
    """The fixture with every edit listed in the record undone: it must equal the base."""
    out = copy.deepcopy(case)
    cid = out["id"]
    out.pop("gold_revision", None)
    for item in (i for i in RECORD["applied"] if i["case"] == cid):
        gold = next(e for e in out["expected"] if e["eval_type"] == item["eval_type"])
        gold.update(item["gold_before"])
        out["should_pass"] = item["should_pass_before"]
        out["slice_tags"]["candidate"] = "good" if item["should_pass_before"] else "flawed"
    for edit in RECORD.get("case_edits", {}).get(cid, []):
        if edit["path"] == "title":
            out["title"] = edit["before"]
        elif edit["path"] == "notes":
            out["notes"] = edit["before"]
        elif edit["path"] == "expected_best_action.why":
            out["expected_best_action"]["why"] = edit["before"]
        else:
            eval_type = edit["path"].split(":")[1].split(".")[0]
            next(e for e in out["expected"] if e["eval_type"] == eval_type)["debate_note"] = edit["before"]
    for edit in (e for e in RECORD.get("context_edits", []) if e["case"] == cid):
        out["context"]["timeline_facts"].pop("notes", None)
    return out


def test_record_states_what_it_is_and_what_it_is_not() -> None:
    assert RECORD["source"] == SOURCE and "NOT human" in RECORD["label_kind"]
    assert "L5" in RECORD["human_agreement"] and "blocked" in RECORD["human_agreement"]
    assert "narrow" in RECORD["blocking_policy"] and "catalog blocking_rule" in RECORD["blocking_policy"]
    counts = RECORD["counts"]
    assert (counts["disputed"], counts["overturned_by_rule"], counts["kept_by_exception"], counts["rejected_by_review"], counts["applied"],
            counts["gold_confirmed"]) == (38, 14, 2, 1, 11, 24)
    assert [len(RECORD[k]) for k in ("applied", "kept_by_exception", "rejected_by_review", "gold_confirmed")] == [11, 2, 1, 24]


def test_the_rule_reproduces_every_decision() -> None:
    for item in RECORD["applied"] + RECORD["kept_by_exception"] + RECORD["rejected_by_review"]:
        assert majority(item["votes"]) != non_fail(item["votes"]["gold"]), item["case"]
    for item in RECORD["gold_confirmed"]:
        assert majority(item["votes"]) == non_fail(item["votes"]["gold"]), item["case"]
    assert {(i["case"], i["eval_type"]) for i in RECORD["kept_by_exception"]} == KEPT


def test_the_rejected_correction_is_documented_and_not_applied() -> None:
    (item,) = RECORD["rejected_by_review"]
    assert (item["case"], item["eval_type"]) == ("beta_cp3_residency_docs_light_ask", "grounding")
    assert "false premises" in item["reason"] and "Leo Park" in item["reason"]
    case = CASES[item["case"]]
    gold = next(e for e in case["expected"] if e["eval_type"] == "grounding")
    assert gold["verdict"] == "pass" and case["should_pass"] and "gold_revision" not in case
    assert "Leo Park" in case["context"]["timeline_facts"]["notes"], "the vendor SE is named in the context"


@pytest.mark.parametrize("item", RECORD["applied"], ids=lambda i: f"{i['case']}:{i['eval_type']}")
def test_applied_correction_is_in_the_fixture_with_a_revision_note(item: dict) -> None:
    case = CASES[item["case"]]
    gold = next(e for e in case["expected"] if e["eval_type"] == item["eval_type"])
    new = item["applied"]
    assert (gold["verdict"], gold["label"], gold["blocking"], gold["rationale"]) == (new["verdict"], new["label"], new["blocking"], new["rationale"])
    assert gold["label"] in EVAL_TYPES[item["eval_type"]]["labels"] or gold["label"] is None
    assert gold["verdict"] == item["votes"]["C_gpt56"] and item["gold_before"]["verdict"] == item["votes"]["gold"]
    note = case["gold_revision"][-1]
    assert note["source"] == SOURCE and note["label_kind"] == "model labels, not human labels"
    change = {"eval_type": item["eval_type"], "from": item["gold_before"]["verdict"], "to": new["verdict"]}
    assert {**change, **({"contested": True} if item.get("contested") else {})} in note["changes"]
    assert any(e["verdict"] == "fail" for e in case["expected"]) != case["should_pass"]


def judgment_of(case: dict, eval_type: str) -> dict:
    return next(e for e in case["expected"] if e["eval_type"] == eval_type)


def gold_problems(cases: dict) -> list[str]:
    """Everything wrong with the legacy gold relative to base plus the record: each recorded field must hold its recorded
    'after' value (diagnostics included), and, with the recorded edits reversed, every one of the 87 cases must hash to its base."""
    problems: list[str] = []
    if set(RECORD["base_canonical_sha256"]) != set(cases):
        return ["the base hashes do not cover exactly the legacy cases"]
    for item in RECORD["applied"]:
        gold = judgment_of(cases[item["case"]], item["eval_type"])
        for key in ("verdict", "label", "diagnostics", "blocking", "rationale"):
            if gold[key] != item["applied"][key]:
                problems.append(f"{item['case']}:{item['eval_type']}.{key} is not the recorded value")
    for cid, edits in RECORD.get("case_edits", {}).items():
        case = cases[cid]
        for edit in edits:
            if edit["path"] == "title":
                now = case["title"]
            elif edit["path"] == "notes":
                now = case["notes"]
            elif edit["path"] == "expected_best_action.why":
                now = case["expected_best_action"]["why"]
            else:
                now = judgment_of(case, edit["path"].split(":")[1].split(".")[0])["debate_note"]
            if now != edit["after"]:
                problems.append(f"{cid} {edit['path']} is not the recorded value")
    for edit in RECORD.get("context_edits", []):
        if cases[edit["case"]]["context"]["timeline_facts"].get("notes") != edit["after"]:
            problems.append(f"{edit['case']} {edit['path']} is not the recorded value")
    problems += [f"{cid} differs from the base by an unrecorded edit" for cid, case in cases.items()
                 if canon(reverse_edits(case)) != RECORD["base_canonical_sha256"][cid]]
    return problems


def test_gold_equals_base_plus_the_record_and_nothing_else() -> None:
    """All 87 legacy cases are hashed against base; recorded fields are checked at their 'after' value before reversing."""
    assert len(CASES) == 87 and gold_problems(CASES) == []
    revised = {cid for cid, c in CASES.items() if "gold_revision" in c}
    assert revised == {i["case"] for i in RECORD["applied"]}


def test_planted_edits_are_caught() -> None:
    """Each edit the first review slipped past is planted in a temp copy and must now fail."""
    untouched = next(cid for cid in CASES if cid not in {i["case"] for i in RECORD["applied"]} | {e["case"] for e in RECORD["context_edits"]})
    item = next(i for i in RECORD["applied"] if i["applied"]["diagnostics"])
    ns = next(iter(RECORD["case_edits"]))

    def plant(mutate) -> list[str]:
        copy_ = copy.deepcopy(CASES)
        mutate(copy_)
        return gold_problems(copy_)

    assert plant(lambda c: c[untouched]["expected"][0].update(rationale="A quietly rewritten rationale.")), "untouched case"
    assert plant(lambda c: judgment_of(c[item["case"]], item["eval_type"]).update(diagnostics=[])), "diagnostics on an applied judgment"
    assert plant(lambda c: c[ns].update(title=c[ns]["title"] + " (edited)")), "a recorded title"
    assert plant(lambda c: c[ns].update(notes="rewritten")), "recorded notes"
    assert plant(lambda c: c[untouched].update(should_pass=not c[untouched]["should_pass"])), "a flipped should_pass"


def test_the_contested_correction_is_flagged_for_a_human() -> None:
    (item,) = [i for i in RECORD["applied"] if i.get("contested")]
    assert item["case"] == "beta_cp4_nonmaterial_fyi_no_action" and item["vote_note"] == "A pass, B fail, G pass, C fail; 2-2 tie broken by C"
    assert "reverses the original rule" in item["contested_reason"]
    case = CASES[item["case"]]
    assert case["expected_best_action"]["action"] == "no_action", "best action is untouched by the contested flip"
    buyer = next(e for e in case["expected"] if e["eval_type"] == "buyer_readiness")
    assert buyer["label"] == "NO_ACTION_NEEDED", "no unrecorded edit"


def test_kept_gold_is_untouched() -> None:
    for case_id, eval_type in KEPT:
        gold = next(e for e in CASES[case_id]["expected"] if e["eval_type"] == eval_type)
        assert gold["verdict"] == "fail" and "gold_revision" not in CASES[case_id]


def test_blocking_stays_narrow_only_the_catalog_rule_blocks() -> None:
    blocking = [i for i in RECORD["applied"] if i["applied"]["blocking"]]
    assert [(i["case"], i["eval_type"]) for i in blocking] == [("beta_cp4_nonmaterial_fyi_no_action", "next_action_quality")]
    declined = [i for i in RECORD["applied"] if "blocking_note" in i]
    assert len(declined) == 2
    for i in RECORD["applied"]:
        entry = EVAL_TYPES[i["eval_type"]]
        assert not i["applied"]["blocking"] or (entry["can_block"] and i["applied"]["verdict"] == "fail")


def test_c_raw_judgments_are_committed_without_case_text() -> None:
    raw = load_json(ROOT / "bench" / "labels" / "adjudication-c-raw-gpt-5.6-sol-pro.json")
    assert len(raw["judgments"]) == 38 and all(set(j) == {"case", "eval_type", "A", "B", "gold", "judge", "C"} for j in raw["judgments"])
    assert all(set(j["C"]) == {"verdict", "label", "blocking", "confidence"} for j in raw["judgments"]), "no rationale, no usage"
    by_key = {(j["case"], j["eval_type"]): j for j in raw["judgments"]}
    for item in RECORD["applied"] + RECORD["kept_by_exception"] + RECORD["rejected_by_review"] + RECORD["gold_confirmed"]:
        assert by_key[(item["case"], item["eval_type"])]["C"]["verdict"] == item["votes"]["C_gpt56"]


def test_apply_script_is_idempotent() -> None:
    spec = importlib.util.spec_from_file_location("apply_adjudication", ROOT / "bench" / "labels" / "apply_adjudication.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    assert module.main(["--check"]) == 0, "the fixtures already carry every correction"
    assert module.patch_line(['  "should_pass": true,'], '"should_pass":', False) == ['  "should_pass": false,']
