"""The mapping of every eval to the canonical three-bucket gate it belongs to (HAR-97 canonical architecture: Bucket 1
context / organizational intelligence B1-B9, Bucket 2 decision / human judgment / action D1-D10, Bucket 3 system / eval
health S1-S5, metrics S6), and for the decision-loop evals (E8-E17) to the object they judge, their trace span and the demo
moment. Transcribed from the HAR-97 coverage table and gate assertions and from section 0 of the decision-evals audit;
contracts/evals/eval_registry.json must carry exactly these values, so a mis-mapped eval fails a test. The E ids stay as
`legacy_id` metadata; no E23 or later id exists."""
from __future__ import annotations

SET, CAND, RANK = ("StrategySet", "id"), ("StrategyCandidate", "candidate_id"), ("DecisionRanking", "id")
HSD, INF, DELTA = ("HumanStrategyDecision", "id"), ("JudgmentInference", "id"), ("HumanDelta", "id")
MUT, DEP, EPI = ("KnowledgeMutation", "id"), ("DependencyInvalidation", "human_delta_id"), ("DecisionEpisode", "id")
RES, GUID, USE, KNOW = ("EvalResult", "id"), ("DecisionGuidance", "id"), ("KnowledgeUse", "id"), ("Knowledge", "id")
ATTR, SPAN = ("AgentRun", "id"), ("TraceSpan", "id")  # a KnowledgeAttribution is the record on its run, keyed by the run

BUCKETS = ("context_intelligence", "decision_action", "system_health")
GATES = {
    **{f"B{i}": "context_intelligence" for i in range(1, 10)},
    **{f"D{i}": "decision_action" for i in range(1, 11)},
    **{f"S{i}": "system_health" for i in range(1, 7)},
}
FLOW = tuple(f"D{i}" for i in range(1, 11))  # Bucket 2's own flow, in order
NOT_BUILT_GATES = ("D6", "D10")  # no eval is registered under them yet: rendered "not measured"

# E1-E7 and E18-E22: family default, with per-eval exceptions.
FAMILY_GATE = {"E1": "B1", "E2": "B2", "E3": "B3", "E4": "B4", "E5": "B5", "E6": "B7", "E7": "D1", "E18": "S1", "E19": "S2",
               "E20": "S3", "E21": "S4", "E22": "S5"}
EVAL_GATE = {
    "E3.1": "B4", "E3.2": "B4", "E3.7": "B4", "E3.8": "B4", "E3.9": "B1",
    "E4.4": "B8", "E4.6": "B8",
    "E5.8": "B6",
    "E7.3": "D3", "E7.4": "D2",
}

# id -> (gate, span_kind, judged_object, demo_moments). M2 chooser, CES choose/edit/send, M3 judgment message, ECOLITE the
# second Play, OFFLINE gold-only, LATER later chronology.
EXPECTED: dict[str, tuple[str, str, tuple[str, str], tuple[str, ...]]] = {
    "E8.1": ("D2", "candidates", SET, ("M2",)), "E8.2": ("D2", "candidates", CAND, ("M2",)),
    "E8.3": ("D2", "candidates", SET, ("M2",)), "E8.4": ("D2", "candidates", SET, ("M2",)),
    "E8.5": ("D2", "candidates", SET, ("M2",)), "E8.6": ("D1", "candidates", SET, ("M2",)),
    "E8.7": ("D2", "candidates", CAND, ("M2",)), "E8.8": ("D2", "candidates", CAND, ("M2",)),
    "E8.9": ("D2", "candidates", CAND, ("M2",)), "E8.10": ("D2", "candidates", CAND, ("M2",)),
    "E8.11": ("D1", "knowledge_used", CAND, ("M2", "ECOLITE")), "E8.12": ("D1", "candidates", CAND, ("M2",)),
    "E8.13": ("D1", "candidates", CAND, ("M2",)),
    "E9.1": ("D3", "ranking", RANK, ("M2",)), "E9.2": ("D3", "ranking", RANK, ("M2", "ECOLITE")),
    "E9.3": ("D3", "ranking", RANK, ("M2",)), "E9.4": ("D3", "ranking", RANK, ("M2",)),
    "E9.5": ("D3", "ranking", RANK, ("M2",)), "E9.6": ("D3", "ranking", RANK, ("M2",)),
    "E9.7": ("D3", "ranking", RANK, ("OFFLINE",)), "E9.8": ("D3", "ranking", RANK, ("M2",)),
    "E10.1": ("D4", "human_interaction", HSD, ("CES",)), "E10.2": ("D5", "human_interaction", INF, ("M3",)),
    "E10.3": ("D5", "human_interaction", DELTA, ("CES", "M3")), "E10.4": ("D5", "human_interaction", INF, ("M3",)),
    "E10.5": ("D5", "human_interaction", INF, ("M3",)), "E10.6": ("D5", "human_interaction", INF, ("M3",)),
    "E10.7": ("D4", "human_interaction", MUT, ("M3",)), "E10.8": ("D9", "human_interaction", EPI, ("LATER",)),
    "E11.1": ("D7", "recomputed_action", DEP, ("CES",)), "E11.2": ("D7", "graph_mutation", DEP, ("OFFLINE",)),
    "E11.3": ("D7", "state", DEP, ("OFFLINE",)), "E11.4": ("D7", "knowledge_used", DEP, ("CES",)),
    "E11.5": ("D7", "ranking", DEP, ("CES",)), "E11.6": ("D7", "recomputed_action", RES, ("CES",)),
    "E11.7": ("D7", "recomputed_action", DEP, ("CES",)), "E11.8": ("D7", "state", DEP, ("CES",)),
    "E11.10": ("D7", "human_interaction", HSD, ("CES",)),
    **{f"E12.{i}": ("D8", "candidates", CAND, ("M2", "CES")) for i in range(1, 11)},
    **{f"E13.{i}": ("D9", "tool_call", SPAN, ("M2", "CES")) for i in range(1, 15)},
    **{f"E14.{i}": ("D9", "execution", HSD, ("CES",)) for i in range(1, 10)},
    **{f"E15.{i}": ("B9", "knowledge_mutation", MUT, ("M3",)) for i in range(1, 14)},
    "E16.1": ("B7", "knowledge_retrieved", ATTR, ("ECOLITE",)),
    "E16.2": ("B7", "knowledge_applicable", GUID, ("ECOLITE",)),
    "E16.3": ("D1", "knowledge_used", USE, ("ECOLITE",)),
    "E16.4": ("D2", "candidates", CAND, ("ECOLITE",)),
    "E16.6": ("B7", "knowledge_applicable", GUID, ("ECOLITE",)),
    "E16.7": ("D5", "human_interaction", INF, ("ECOLITE", "M3")),
    "E16.8": ("B7", "knowledge_applicable", GUID, ("OFFLINE",)),
    **{f"E17.{i}": ("B9", "knowledge_mutation", KNOW, ("ECOLITE", "LATER")) for i in range(1, 11)},
}
# Evals that also belong to a second gate (a candidate's artifact is judged before the human chooses and again as the final one).
ALSO_GATES = {f"E12.{i}": ["D2"] for i in range(1, 11)}
ALSO_GATES.update({"E8.11": ["B7"], "E11.6": ["D8"], "E16.3": ["D2", "D3"]})
ALSO_SPANS = {f"E12.{i}": ["recomputed_action"] for i in range(1, 11)}
HIDDEN = ("E16.5", "E22.4")
HIDDEN_GATE = {"E16.5": "D2", "E22.4": "S5"}
REBUILD = ("E8.1", "E8.2", "E8.11", "E9.5", "E13.4", "E14.7")  # wrong implementation pointers: none (rebuild)
DEMO_MOMENTS = ("M2", "CES", "M3", "ECOLITE", "OFFLINE", "LATER")


def gate_of(eval_id: str) -> str:
    """The gate of any registry eval, by the tables above."""
    if eval_id in HIDDEN_GATE:
        return HIDDEN_GATE[eval_id]
    if eval_id in EXPECTED:
        return EXPECTED[eval_id][0]
    return EVAL_GATE.get(eval_id) or FAMILY_GATE[eval_id.split(".")[0]]
