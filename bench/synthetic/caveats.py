"""The A/B/C caveats every report on the synthetic layer must carry (spec section j, caveats 1 to 4).

The future A/B/C report generator (HAR-129 / HAR-128 harness) MUST render `caveat_block()` and call
`assert_carries_caveats(report_text)` before it publishes: a report without them is refused. The wording is a
constant here so the spec and every report quote the same sentences (tested against the spec).
"""
from __future__ import annotations

ABC_CAVEATS: tuple[tuple[str, str], ...] = (
    ("synthetic_outcomes",
     "Synthetic outcomes: outcomes are drawn from planted rules, so a learner recovering them shows the loop recovers "
     "planted rules, not that they hold in real selling. Uplift is scored on the 4 hidden rules only."),
    ("distinguishable_data",
     "Distinguishable data: before the paraphrase pass a classifier separates real from synthetic emails at AUC 0.998 "
     "(cc is a residual tell); any after-paraphrase AUC is reported next to this number, never instead of it."),
    ("noisy_mode",
     "Noisy mode: the frozen seed fails the noisy mode (learner AUC 0.546) and the real-base noisy seed pass rate is "
     "0.075, so learning from real extraction may be weak."),
    ("real_base_exception",
     "Real-base exception: the real-base sweep passes only under a recorded user exception (clean seed pass rate "
     "0.425; 23 of 40 seeds fail a per-seed gate, mostly oracle AUC below 0.65). Eval-known rules "
     "(quote_before_economic_buyer, requested_pause_ignored, written_handover_exempts) are not recovered reliably and "
     "back no learning claim; hidden-rule and null gates still apply."),
)


def caveat_block() -> str:
    """The caveats as a numbered block to paste into a report."""
    return "\n".join(f"{i}. {text}" for i, (_, text) in enumerate(ABC_CAVEATS, 1))


def assert_carries_caveats(report_text: str) -> None:
    """Raise when a report omits any caveat (the future A/B/C report generator calls this before publishing)."""
    missing = [key for key, text in ABC_CAVEATS if text not in report_text]
    if missing:
        raise ValueError(f"report is missing the mandatory A/B/C caveats: {missing}")
