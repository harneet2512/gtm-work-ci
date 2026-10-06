"""WP18 (HAR-116): §20 metrics and the L5 table on hand-computed judge-vs-gold pairs (neutral ids)."""
from __future__ import annotations

from ghost_worker.judges.engine import JudgeOutcome
from ghost_worker.judges.meta import HUMAN_BLOCKED, L5_ROWS, l5_table
from ghost_worker.judges.metrics import (Scored, case_level, consistency, group_stats, judgment_stats, rate,
                                         slice_agreement)


def gold(verdict: str, label: str | None = None, blocking: bool = False, diagnostics: tuple = ()) -> dict:
    return {"verdict": verdict, "label": label, "blocking": blocking, "diagnostics": list(diagnostics)}


def said(eval_type: str, verdict: str, label: str | None = None, blocking: bool = False, trial: int = 1,
         diagnostics: tuple = ()) -> JudgeOutcome:
    return JudgeOutcome(eval_type=eval_type, trial=trial, result={
        "verdict": verdict, "label": label, "blocking": blocking, "diagnostics": list(diagnostics)})


def pair(case: str, eval_type: str, g: dict, outcome: JudgeOutcome | None, *, trial: int = 1,
         should_pass: bool = False, motion: str = "expansion") -> Scored:
    return Scored(case_id=case, eval_type=eval_type, trial=trial, gold=g, slices={"motion": motion},
                  should_pass=should_pass, outcome=outcome)


SCORED = [
    pair("c1", "buyer_readiness", gold("fail", "TOO_EARLY", True, ("too_early",)),
         said("buyer_readiness", "fail", "TOO_EARLY", True, diagnostics=("too_early",))),      # true block
    pair("c1", "grounding", gold("fail"), said("grounding", "pass")),                          # false pass
    pair("c2", "buyer_readiness", gold("pass", "READY"), said("buyer_readiness", "fail", "TOO_EARLY", True),
         should_pass=True, motion="renewal"),                                                    # false block
    pair("c2", "grounding", gold("pass"), said("grounding", "pass"), should_pass=True, motion="renewal"),
    pair("c3", "momentum", gold("warn", "COOLING"), JudgeOutcome(eval_type="momentum", trial=1, error="X: y")),
    pair("c3", "grounding", gold("pass"), None),
]


def test_rate_is_none_without_a_denominator() -> None:
    assert rate(0, 0) is None and rate(1, 3) == 0.333


def test_judgment_stats_on_hand_computed_pairs() -> None:
    s = judgment_stats(SCORED)
    assert (s["n_gold"], s["n_judged"], s["error_count"], s["skipped_count"], s["coverage"]) == (6, 4, 1, 1, 0.667)
    assert s["insufficient_data"] is False
    assert s["verdict_agreement"] == 0.5 and s["label_agreement"] == 0.5 and s["n_labelled"] == 2
    assert (s["gold_fail"], s["judge_fail"], s["fail_precision"], s["fail_recall"]) == (2, 2, 0.5, 0.5)
    assert s["false_pass_rate"] == 0.5
    assert (s["false_block_rate"], s["block_precision"], s["block_recall"]) == (0.333, 0.5, 1.0)
    assert s["diagnostic_recall"] == 1.0 and s["abstain_rate"] == 0.0
    assert s["verdict_kappa"] == 0.0


def test_kappa_is_one_for_perfect_and_none_for_degenerate() -> None:
    perfect = [pair("a", "g", gold("pass"), said("g", "pass")), pair("b", "g", gold("fail"), said("g", "fail"))]
    assert judgment_stats(perfect)["verdict_kappa"] == 1.0
    assert judgment_stats(perfect[:1])["verdict_kappa"] is None
    assert judgment_stats([])["verdict_agreement"] is None


def test_case_level_false_block_and_false_pass() -> None:
    c = case_level(SCORED)
    assert (c["good_runs"], c["false_block_rate"]) == (1, 1.0)  # c2 should pass but was blocked
    assert (c["flawed_runs"], c["false_pass_rate"]) == (1, 0.0)  # c1 has a gold fail and a judge fail


def test_consistency_across_trials() -> None:
    trials = [pair("c1", "grounding", gold("fail"), said("grounding", "fail", trial=t), trial=t) for t in (1, 2)]
    trials.append(pair("c1", "grounding", gold("fail"), said("grounding", "pass", trial=3), trial=3))
    trials += [pair("c2", "grounding", gold("pass"), said("grounding", "pass", trial=t), trial=t) for t in (1, 2)]
    k = consistency(trials)
    assert (k["n_repeated"], k["unanimous_rate"], k["pairwise_agreement"]) == (2, 0.5, 0.5)
    assert consistency(SCORED)["n_repeated"] == 0 and consistency(SCORED)["unanimous_rate"] is None


def test_grouping_and_slices() -> None:
    per_eval = group_stats(SCORED, lambda s: s.eval_type)
    assert per_eval["grounding"]["n_gold"] == 3 and per_eval["grounding"]["false_pass_rate"] == 1.0
    slices = slice_agreement(SCORED, ["motion"])
    assert slices["motion"]["renewal"]["verdict_agreement"] == 0.5
    assert set(slices["motion"]["renewal"]) == {"n_judged", "verdict_agreement", "label_agreement",
                                                "false_pass_rate", "false_block_rate"}


def test_l5_reports_gold_agreement_and_blocks_human_agreement() -> None:
    rows = {r["dimension"]: r for r in l5_table(SCORED)}
    assert [r.dimension for r in L5_ROWS] == list(rows)
    assert rows["readiness"]["gold_n"] == 2 and rows["readiness"]["gold_verdict_agreement"] == 0.5
    assert rows["readiness"]["status"] == HUMAN_BLOCKED and rows["readiness"]["human_agreement"] is None
    assert rows["knowledge extraction"]["eval_types"] == [] and "out_of_scope" in rows["knowledge extraction"]["status"]
    human = {"readiness": [pair("h1", "buyer_readiness", gold("pass"), said("buyer_readiness", "pass"))]}
    measured = {r["dimension"]: r for r in l5_table(SCORED, human)}["readiness"]
    assert (measured["human_n"], measured["human_agreement"]) == (1, 1.0)


def errored(case: str, g: dict, *, trial: int = 1, should_pass: bool = False) -> Scored:
    return pair(case, "grounding", g, JudgeOutcome(eval_type="grounding", trial=trial, error="LLMError: x"),
                trial=trial, should_pass=should_pass)


def test_case_level_refuses_to_report_rates_when_nothing_was_judged() -> None:
    rows = [errored("c1", gold("fail")), pair("c1", "momentum", gold("warn"), None),
            errored("c2", gold("pass"), should_pass=True)]
    c = case_level(rows)
    assert c["insufficient_data"] is True
    assert c["false_pass_rate"] is None and c["false_block_rate"] is None
    assert (c["good_runs"], c["flawed_runs"]) == (0, 0)
    assert (c["error_count"], c["skipped_count"]) == (2, 1)


def test_case_level_excludes_errored_and_skipped_rows_from_denominators() -> None:
    rows = [errored("c1", gold("fail")),                                         # flawed, but its judge errored
            pair("c2", "grounding", gold("fail"), said("grounding", "pass")),    # judged false pass
            pair("c3", "grounding", gold("pass"), said("grounding", "pass"), should_pass=True),
            errored("c4", gold("pass"), should_pass=True)]                       # good, but errored: not "not blocked"
    c = case_level(rows)
    assert c["insufficient_data"] is False
    assert (c["flawed_runs"], c["false_pass_rate"]) == (1, 1.0)
    assert (c["good_runs"], c["false_block_rate"]) == (1, 0.0)
    assert (c["error_count"], c["skipped_count"], c["unjudged_runs"]) == (2, 0, 2)


def test_judgment_stats_flags_insufficient_data_when_nothing_judged() -> None:
    s = judgment_stats([errored("c1", gold("fail")), pair("c1", "momentum", gold("warn"), None)])
    assert s["insufficient_data"] is True and s["n_judged"] == 0
    assert s["false_pass_rate"] is None and s["verdict_agreement"] is None and s["coverage"] == 0.0
    assert (s["error_count"], s["skipped_count"]) == (1, 1)


def test_false_pass_counts_only_fails_on_dimensions_the_gold_judged_as_fail() -> None:
    """A judge failing an unrelated dimension (gold: pass) is not catching the flaw; the any-fail view is kept
    beside it so both are visible."""
    rows = [pair("c1", "grounding", gold("fail"), said("grounding", "pass")),
            pair("c1", "momentum", gold("pass"), said("momentum", "fail"))]
    c = case_level(rows)
    assert (c["flawed_runs"], c["false_pass_rate"]) == (1, 1.0)
    assert c["false_pass_rate_any_flag"] == 0.0
    caught = [pair("c2", "grounding", gold("fail"), said("grounding", "fail"))]
    assert case_level(caught)["false_pass_rate"] == 0.0
