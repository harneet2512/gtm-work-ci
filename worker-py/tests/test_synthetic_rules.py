"""WP32 (HAR-131): the planted-rule registry bench/synthetic/rules.v1.json (spec v1.1) is schema-valid
and internally consistent. Spec: docs/data/synthetic-layer-v1.md."""
from __future__ import annotations

import copy
import json
import math
import sys
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import rules as R  # noqa: E402

RULES = json.loads(R.RULES_PATH.read_text(encoding="utf-8"))
SCHEMA = json.loads(R.SCHEMA_PATH.read_text(encoding="utf-8"))
BASE_RATES = json.loads((R.HERE / RULES["base_rates_file"]).read_text(encoding="utf-8"))


def validator(ref: str | None = None) -> Draft202012Validator:
    return Draft202012Validator(SCHEMA if ref is None else {"$ref": ref, "$defs": SCHEMA["$defs"]})


def test_schema_is_valid_and_both_rule_files_validate() -> None:
    Draft202012Validator.check_schema(SCHEMA)
    for doc, v in ((RULES, validator()), (BASE_RATES, validator("#/$defs/baseRatesFile"))):
        errors = list(v.iter_errors(doc))
        assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]


def _rule(doc: dict, key: str) -> dict:
    return next(r for r in doc["rules"] if r["key"] == key)


@pytest.mark.parametrize(("mutate", "why"), [
    (lambda d: d["rules"][0].update(kind="vibes"), "unknown rule kind"),
    (lambda d: d["rules"][0].pop("log_odds"), "every rule states its effect size"),
    (lambda d: d["rules"][0].update(key="R1"), "keys carry the syn1_ prefix (leak marker)"),
    (lambda d: d.update(provenance="synthetic"), "provenance is versioned"),
    (lambda d: d["rules"][0]["exercises"].update(evals=["Vibes Eval"]), "eval names are snake_case"),
    (lambda d: d["rules"][0]["exercises"].update(timescale=["eventually"]), "HAR-97 §12 timescale"),
    (lambda d: d.update(extra=1), "no unknown top-level keys"),
    (lambda d: d["rules"][0].pop("encoded_by"), "an eval_known rule names what encodes it"),
    (lambda d: _rule(d, "syn1_ops_contact_before_quote").pop("audit"), "a hidden rule carries its audit"),
    (lambda d: _rule(d, "syn1_ops_contact_before_quote").update(key_terms=["x1"]), "a hidden rule lists key terms"),
    (lambda d: _rule(d, "syn1_null_industry").update(group="eval_known"), "nulls are the control group"),
    (lambda d: _rule(d, "syn1_ops_contact_before_quote").pop("decision"), "a hidden rule names the agent decision it changes"),
    (lambda d: d.pop("generation_seed"), "one frozen generation seed"),
    (lambda d: d["scoring"].update(uplift_groups=["eval_known"]), "uplift is scored on hidden rules only"),
])
def test_known_bad_rule_files_are_rejected(mutate, why: str) -> None:
    doc = copy.deepcopy(RULES)
    mutate(doc)
    assert list(validator().iter_errors(doc)), why


def test_an_unsourced_base_rate_must_say_it_is_assumed() -> None:
    doc = copy.deepcopy(BASE_RATES)
    doc["base_rates"][0].update(source=None, assumed=False)
    assert list(validator("#/$defs/baseRatesFile").iter_errors(doc))


def test_loader_returns_immutable_rules_and_the_frozen_seed() -> None:
    rs = R.load_rules()
    assert rs.version == "synthetic_rules:v1" and rs.provenance == "synthetic:v1"
    assert rs.generation_seed == RULES["generation_seed"] and "never" in RULES["seed_policy"]
    with pytest.raises(AttributeError):
        rs.rules[0].log_odds = 0.0  # type: ignore[misc]


def test_odds_ratio_matches_log_odds_and_null_rules_have_no_effect() -> None:
    for rule in R.load_rules().rules:
        assert math.isclose(rule.odds_ratio, math.exp(rule.log_odds), rel_tol=0.01), rule.key
        if rule.kind == "null":
            assert rule.log_odds == 0.0 and rule.group == "control", rule.key


def test_groups_split_eval_known_from_at_least_four_hidden_rules() -> None:
    rules = R.load_rules().rules
    hidden = [r for r in rules if r.group == "hidden_company_specific"]
    assert len(hidden) >= 4 and all(r.kind == "effect" for r in hidden)
    assert any(r.log_odds > 0 for r in hidden) and any(r.log_odds < 0 for r in hidden)
    assert [r for r in rules if r.group == "eval_known"] and sum(r.kind == "null" for r in rules) >= 4
    assert RULES["scoring"]["uplift_groups"] == ["hidden_company_specific"]


def test_hidden_effects_stay_within_plausible_bounds() -> None:
    """Lead policy 2026-10-02: hidden effects may be raised only up to an odds ratio of 3.0 (or down to 0.33)."""
    for rule in R.load_rules().rules:
        if rule.group == "hidden_company_specific":
            assert 1 / 3.0 - 1e-3 <= rule.odds_ratio <= 3.0, rule.key


def test_exceptions_modify_an_existing_effect_rule() -> None:
    rs = R.load_rules()
    effects = {r.key for r in rs.rules if r.kind == "effect"}
    for rule in rs.rules:
        assert (rule.modifies is not None) == (rule.kind == "exception"), rule.key
        if rule.modifies:
            assert rule.modifies in effects, rule.key


TRANSITIONS = ROOT / "contracts" / "transitions" / "rules.v1.json"


def test_exercised_transition_facts_exist_in_the_transition_rules() -> None:
    """Fact keys come from ADR-0012 (PR #16). Until that contract lands on main the snapshot in
    rules.v1.json#transition_facts is the reference; once it lands the snapshot must equal it."""
    snapshot = {t: set(f) for t, f in RULES["transition_facts"].items()}
    if TRANSITIONS.exists():
        contract = json.loads(TRANSITIONS.read_text(encoding="utf-8"))
        assert snapshot == {t["id"]: {f["key"] for f in t["facts"] + t["contradictions"]} for t in contract["transitions"]}
    for rule in R.load_rules().rules:
        for ref in rule.transitions:
            assert ref["transition"] in snapshot and set(ref["facts"]) <= snapshot[ref["transition"]], rule.key


def test_every_effect_rule_states_a_prevalence() -> None:
    for rule in R.load_rules().rules:
        if rule.kind == "effect":
            assert 0 < rule.prevalence_target < 1, rule.key


def test_learnability_gates_bracket_a_detectable_but_not_trivial_signal() -> None:
    g = R.load_rules().gates
    assert 0.5 < g["learner_auc"][0] < g["learner_auc"][1] <= g["oracle_auc"][1] < 0.9
    assert 0.5 < g["hidden_learner_auc"][0] and g["null_incremental_auc_max"] <= 0.05
    assert g["hidden_sign_recovery_min"] >= 0.9


def test_planted_log_odds_sums_effects_and_ignores_nulls() -> None:
    rs = R.load_rules()
    base = rs.outcome["base_log_odds"]
    assert R.planted_log_odds(rs, set()) == base
    assert R.planted_log_odds(rs, {r.key for r in rs.rules if r.kind == "null"}) == base
    one = next(r for r in rs.rules if r.kind == "effect")
    assert math.isclose(R.planted_log_odds(rs, {one.key}), base + one.log_odds)


def test_an_exception_cancels_the_effect_it_modifies_and_alone_does_nothing() -> None:
    rs = R.load_rules()
    exc = next(r for r in rs.rules if r.kind == "exception")
    base = rs.outcome["base_log_odds"]
    assert math.isclose(R.planted_log_odds(rs, {exc.modifies, exc.key}), base)
    assert R.planted_log_odds(rs, {exc.key}) == base


def test_unknown_feature_keys_are_rejected() -> None:
    with pytest.raises(KeyError):
        R.planted_log_odds(R.load_rules(), {"syn1_not_a_rule"})


def test_a_frozen_rule_set_cannot_change_silently() -> None:
    """Draft while the spec is under review; once frozen (first generation) the content hash is pinned."""
    rs = R.load_rules()
    if rs.status == "frozen":
        assert R.content_hash(RULES) == RULES["frozen_sha256"]
    else:
        assert rs.status == "draft"
