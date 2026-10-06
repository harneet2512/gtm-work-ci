"""L5 meta-evals: grader agreement per HAR-97 L5 dimension.

HAR-97 L5: "For every important semantic eval, create a small expert-labeled set. Have human reviewers label the
cases independently, then measure automated grader agreement." The only labels that exist today are the legacy
fixture gold (single-author, written by the WP16 agent, invented demo accounts). This table therefore reports
agreement with that gold now and marks agreement with HUMAN labels as blocked until a human review exists.
"""
from __future__ import annotations

from collections.abc import Mapping, Sequence
from typing import Any

from pydantic import BaseModel, ConfigDict

from .metrics import Scored, judgment_stats

GOLD_PROVENANCE = "legacy fixture gold (invented demo accounts; to be replaced by CRMArena-based gold)"
HUMAN_BLOCKED = ("blocked: no human-labelled set exists yet (the legacy gold is single-author, not an independent "
                 "human review)")


class L5Row(BaseModel):
    """One HAR-97 L5 dimension (line numbered as in docs/traceability/wp18_har97_lines.txt)."""

    model_config = ConfigDict(frozen=True)

    line: int
    text: str
    dimension: str
    eval_types: tuple[str, ...]
    blocked_reason: str | None = None


L5_ROWS: tuple[L5Row, ...] = (
    L5Row(line=3151, text="* readiness", dimension="readiness", eval_types=("buyer_readiness",)),
    L5Row(line=3152, text="* next-step quality", dimension="next-step quality", eval_types=("next_step_quality",)),
    L5Row(line=3153, text="* stakeholder coverage", dimension="stakeholder coverage",
          eval_types=("stakeholder_coverage",)),
    L5Row(line=3154, text="* champion handling", dimension="champion handling",
          eval_types=("champion_strength", "champion_continuity")),
    L5Row(line=3155, text="* economic-buyer coverage", dimension="economic-buyer coverage",
          eval_types=("economic_buyer_coverage",)),
    L5Row(line=3156, text="* decision process", dimension="decision process", eval_types=("decision_process",)),
    L5Row(line=3157, text="* business case", dimension="business case", eval_types=("business_case",)),
    L5Row(line=3158, text="* expansion readiness", dimension="expansion readiness",
          eval_types=("expansion_readiness",)),
    L5Row(line=3159, text="* knowledge applicability", dimension="knowledge applicability",
          eval_types=("knowledge_applicability",)),
    L5Row(line=3160, text="* knowledge extraction", dimension="knowledge extraction", eval_types=(),
          blocked_reason=("no grader and no gold yet: knowledge-extraction fidelity is an L4 learner eval, "
                          "out_of_scope in the eval catalog (l4_knowledge_learning, WP20/HAR-118); the same "
                          "agreement code applies once its grader and labels exist")),
)


def l5_table(scored: Sequence[Scored], human_labels: Mapping[str, Sequence[Scored]] | None = None) -> list[dict[str, Any]]:
    """One row per L5 dimension: agreement with the legacy gold now; human agreement when `human_labels`
    (dimension -> scored pairs against human labels) is supplied, otherwise blocked."""
    rows = []
    for row in L5_ROWS:
        pairs = [s for s in scored if s.eval_type in row.eval_types]
        gold = judgment_stats(pairs)
        human = (human_labels or {}).get(row.dimension)
        rows.append({
            "line": row.line, "dimension": row.dimension, "eval_types": list(row.eval_types),
            "gold_n": gold["n_judged"], "gold_verdict_agreement": gold["verdict_agreement"],
            "gold_label_agreement": gold["label_agreement"], "gold_false_pass_rate": gold["false_pass_rate"],
            "gold_false_block_rate": gold["false_block_rate"],
            "human_n": len(human) if human else 0,
            "human_agreement": judgment_stats(human)["verdict_agreement"] if human else None,
            "status": row.blocked_reason or ("measured against human labels" if human else HUMAN_BLOCKED),
        })
    return rows
