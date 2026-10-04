"""WP16 (HAR-114): the benchmark reads its vocabulary from contracts/evals/eval_catalog.json (catalog completeness is
tested in test_eval_catalog_contract.py) and as a whole covers what HAR-97 §20 asks for (situations, good and bad candidates,
false-block measurability, slices, difficulty, gold for every GTM eval the demo relies on)."""
from __future__ import annotations

from collections import Counter

from eval_cases_lib import (CASE_SCHEMA, CASE_TYPES, CASES, EVAL_TYPES, IDS, PEOPLE_BY_ID, SYN_PREFIX,
                            eval_type_table, format_eval_type_table, format_table, judgments, summary_table)
from test_contracts import EXAMPLES, SCHEMAS, load_json
from test_fixtures import EVENTS, FIXTURES

# Anchor cases HAR-114 names explicitly.
REQUIRED_CASES = {
    "acme_cp4_draft1_champion_bypass": "Acme Draft-1 champion bypass",
    "northstar_cp4_k17_delegation_exception_honored": "Northstar delegation exception",
    "acme_variant_priya_departed_k17_trap": "K17 offered but champion departed",
    "acme_variant_new_business_k17_cited": "K17 offered but motion = new_business",
}


# ---------- catalog ----------


def test_catalog_has_the_seventeen_har97_situations() -> None:
    assert len(CASE_TYPES) == 17 and len(set(CASE_TYPES)) == 17


def test_case_schema_copies_no_catalog_vocabulary() -> None:
    text = (FIXTURES / "evals" / "case.schema.json").read_text(encoding="utf-8")
    assert "labelVocabulary" not in CASE_SCHEMA["$defs"] and "enum" not in CASE_SCHEMA["$defs"]["caseType"]
    for entry in EVAL_TYPES.values():
        for label in entry["labels"]:
            assert f'"{label}"' not in text, f"label {label} is copied into the case schema"
    assert not any(f'"{t}"' in text for t in CASE_TYPES), "case types are copied into the case schema"








# ---------- registry and coverage ----------
def test_ids_registry_covers_world_and_is_unique() -> None:
    expected = {p for p in EVENTS if not p.endswith("_dup.json")}
    assert set(IDS["activities"]) == expected
    assert len(set(IDS["activities"].values())) == len(expected)
    assert not any(v.startswith(SYN_PREFIX) for v in IDS["activities"].values())
    assert len(PEOPLE_BY_ID) == len(IDS["people"])
    for key, person in IDS["people"].items():
        assert person["account"] in {"org", *IDS["accounts"]}, key
    org = {p["key"] for p in load_json(FIXTURES / "world/org.json")["people"]}
    assert org == {k for k, v in IDS["people"].items() if v["account"] == "org"}
    assert IDS["knowledge"]["K17@v1"] == load_json(EXAMPLES / "knowledge.example.json")["id"]


def test_coverage_of_har97_situations() -> None:
    ids = [c["id"] for c in CASES.values()]
    assert len(ids) == len(set(ids)) and len(ids) >= 51
    counts = Counter(c["case_type"] for c in CASES.values())
    assert {t: counts[t] for t in CASE_TYPES if counts[t] < 3} == {}
    flawed = Counter(c["case_type"] for c in CASES.values() if not c["should_pass"])
    assert all(flawed[t] >= 1 for t in CASE_TYPES), "every situation needs a failing candidate"
    good = [c for c in CASES.values() if c["should_pass"]]
    assert len(good) >= 12, "false-block measurement needs good candidates"
    best = Counter(c["expected_best_action"]["action"] for c in CASES.values())
    # Gold v2 (adjudication 2026-10-02) turned the legacy good no_action control into a flawed one; the should-pass
    # no_action coverage now comes from the CRMArena gold (test_crmarena_gold.py covers both sets together).
    assert best["wait"] >= 3 and best["no_action"] >= 3, best
    assert {c["candidate_action"]["proposed_action_type"] for c in good} >= {"wait", "send_email", "schedule_meeting"}
    assert set(REQUIRED_CASES) <= set(CASES), set(REQUIRED_CASES) - set(CASES)
    origins = Counter(c["slice_tags"]["origin"] for c in CASES.values())
    assert origins["checkpoint"] >= 20 and origins["synthetic_variant"] >= 10, origins
    assert {c["based_on"]["account"] for c in CASES.values()} == set(IDS["accounts"])


def test_summary_tables_account_for_every_case_and_judgment() -> None:
    for by in ("case_type", "difficulty", "account", "origin", "motion", "stage", "champion_status", "economic_buyer",
               "risk", "candidate"):
        rows = summary_table(by)
        assert sum(r[1] for r in rows) == len(CASES)
        assert sum(r[2] + r[3] for r in rows) == len(CASES)
    rows = eval_type_table()
    assert sum(r[3] for r in rows) == len(judgments())
    assert all(r[3] == sum(r[4:8]) for r in rows)
    print("\n" + format_table("case_type") + "\n\n" + format_table("difficulty") + "\n\n" + format_eval_type_table())


# ---------- review-driven coverage (HAR-114 rework) ----------
def test_false_blocks_are_measurable_for_every_semantic_eval_with_gold() -> None:
    verdicts: dict[str, Counter] = {}
    for _, e in judgments():
        verdicts.setdefault(e["eval_type"], Counter())[e["verdict"]] += 1
    thin = {t: dict(v) for t, v in verdicts.items() if EVAL_TYPES[t]["kind"] == "semantic" and v["pass"] < 2}
    assert thin == {}, "every semantic eval with gold needs >= 2 pass judgments"


def test_gtm_evals_have_pass_and_fail_gold() -> None:
    for eval_type in ("business_case", "action_stage_fit", "expansion_readiness", "provenance_coverage", "momentum",
                      "champion_strength", "customer_risk_sensitivity"):
        verdicts = Counter(e["verdict"] for _, e in judgments() if e["eval_type"] == eval_type)
        assert sum(verdicts.values()) >= 2 and verdicts["pass"] >= 1 and verdicts["fail"] >= 1, (eval_type, verdicts)


def test_slices_beyond_expansion_and_low_risk_are_populated() -> None:
    for tag, value in (("motion", "new_business"), ("motion", "renewal"), ("risk", "high")):
        cases = [c for c in CASES.values() if c["slice_tags"][tag] == value]
        assert len(cases) >= 3 and len({c["based_on"]["account"] for c in cases}) >= 2, (tag, value)
    assert all(c["based_on"]["kind"] == "synthetic_variant" for c in CASES.values()
               if c["slice_tags"]["motion"] != "expansion"), "non-expansion slices are clearly marked variants"


def test_difficulty_spread_and_trigger_concentration() -> None:
    difficulty = Counter(c["difficulty"] for c in CASES.values())
    assert difficulty["hard"] >= 20 and difficulty["easy"] >= 10, difficulty
    triggers = Counter((c["based_on"]["account"], c["context"]["trigger"]["activity_id"]) for c in CASES.values())
    assert max(triggers.values()) <= 0.2 * len(CASES), triggers.most_common(3)


def test_conflicts_follow_the_standing_order() -> None:
    """ADR-0008/0009 (cases): a surfaced conflict's winner outranks the contradicting claim in the five-value standing order."""
    order = load_json(SCHEMAS / "common.v1.json")["$defs"]["standing"]["enum"]
    assert order == ["human_approved", "crm_explicit", "first_party_record", "first_party_ai", "third_party"]
    conflicts = [x for c in CASES.values() for x in c["context"]["state"].get("conflicts", [])]
    assert conflicts  # gold conflicts: test_fixtures.test_gold_conflicts_follow_the_standing_order
    for x in conflicts:
        assert order.index(x["winner_standing"]) < order.index(x["contradicting_standing"]), x["field"]


def test_buyer_readiness_blocks_only_as_too_early() -> None:
    """Catalog rule: a buyer_readiness block is always a TOO_EARLY push past an open technical or security gate."""
    blocks = [(c["id"], e["label"]) for c, e in judgments() if e["eval_type"] == "buyer_readiness" and e["blocking"]]
    assert blocks and all(label == "TOO_EARLY" for _, label in blocks), blocks
