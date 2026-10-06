// HAR-149: the stored gate results and the stored ranking the fixture core serves for the MedTech episode, shaped like a real
// run's (per-criterion results, control effect, evaluator version, lineage, judge latency). Synthetic like every fixture
// (NOTICE): the MedTech wording follows the recorded episode; no live model produced any of it.

const ID = (n) => `0e9ead00-0000-4000-8000-0000000d${String(n).padStart(4, "0")}`;
const MODEL = { kind: "model" };
const crit = (id, result, why, refs) => ({ id, label: id, result, why, evidence_refs: result === "unknown" ? [] : refs });

/** The gate's effect for a verdict, as bucket2.ControlEffect gives it (parity-tested in Go against the registry). */
function effect(gate, sub, kind, verdict) {
  const blocker = gate === "D8" && kind === "deterministic" && ["recipients", "no_stale_content"].includes(sub);
  if (blocker && verdict === "pass") return "CONTINUE";
  if (blocker && verdict === "fail") return "BLOCK";
  return verdict === "unknown" ? "MARK UNKNOWN" : "RECORD ONLY";
}

export function medtechInspectorResults(e) {
  const trig = e.trace.trigger_activities[0].id;
  const [a, b, c] = [...e.strategies.strategy_set.candidates].sort((x, y) => x.ranking - y.ranking).map((x) => x.candidate_id);
  const setId = e.strategies.strategy_set.id;
  const act = `activity:${trig}`;
  let n = 10;
  const row = (gate, sub, label, object, span, verdict, observed, why, refs, criteria, grader, extra = {}) => {
    n += 1;
    return {
      id: ID(n), gate, sub_gate: sub, label, judged_object: object, span_id: span, verdict,
      question: "", observed, why, evidence_refs: refs, improves: "", grader, calibrated: false,
      criteria, control_effect: effect(gate, sub, grader.kind, verdict),
      evaluator_version: grader.kind === "model" ? grader.prompt_version ?? `${gate}:model:unversioned` : `${gate}:deterministic:v1`,
      lineage: {}, latency_ms: null, model_calls: null, tokens: null, cost_usd: null, ...extra,
    };
  };
  const cand = (id) => ({ type: "StrategyCandidate", id });
  const set = { type: "DecisionRanking", id: setId };
  const dims = (list, refs) => list.map(([id, result, why]) => crit(id, result, why, refs));
  const D2 = (id, rows, verdict) =>
    row("D2", "candidate", "", cand(id), `candidates:${setId}`, verdict, rows.map(([k, r]) => `${k}=${r}`).join(", "),
      rows.filter(([, r]) => r !== "pass").map(([k, , w]) => `${k}: ${w}`).join("; ") || "every judged dimension passed with evidence", [`candidate:${id}`, act],
      dims(rows.map(([k, r, w]) => [k, r, w ?? "holds"]), [`candidate:${id}`, act]), { ...MODEL, prompt_version: "candidate_quality:v1" });
  return [
    row("B5", "", "", { type: "Episode", id: e.episodeId }, `precedents:${e.episodeId}`, "warn", "3 precedents retrieved; 1 relevant case may be missing",
      "relevance_and_misses: a relevant earlier case about onboarding fees was not retrieved", [act],
      dims([["no_future_leakage", "pass", "holds"], ["situational_not_textual", "pass", "holds"], ["relevance_and_misses", "warn", "a relevant earlier case about onboarding fees was not retrieved"]], [act]),
      { ...MODEL, prompt_version: "b5_precedent_relevance:v1" }),
    row("D1", "", "", { type: "StrategySet", id: setId }, `candidates:${setId}`, "pass", "intent_follows_state=pass, timing=pass, quiet_move_considered=pass", "every judged dimension passed with evidence", [act],
      dims([["intent_follows_state", "pass", "holds"], ["timing", "pass", "holds"], ["quiet_move_considered", "pass", "holds"]], [act]), { ...MODEL, prompt_version: "intent_fit:v1" }),
    D2(a, [["fit", "pass"], ["grounding", "pass"], ["cta", "warn", "the ask is firmer than the buyer's stated timing"], ["recipients", "pass"], ["timing", "pass"]], "warn"),
    D2(b, [["fit", "pass"], ["grounding", "pass"], ["cta", "pass"], ["recipients", "pass"], ["timing", "pass"]], "pass"),
    D2(c, [["fit", "warn", "it answers a competitor claim the buyer did not make"], ["grounding", "pass"], ["factual_integrity", "fail", "it states onboarding parity that no cited evidence supports"], ["recipients", "pass"]], "fail"),
    row("D3", "ranking", "", set, `ranking:${setId}`, "warn", "supported_by_evidence_state=pass, uncertainty_reflected=warn, no_blocked_preferred=pass",
      "uncertainty_reflected: the ranking is more certain than the open cost question allows", [`candidate:${a}`, act],
      dims([["supported_by_evidence_state", "pass", "holds"], ["supported_by_knowledge", "pass", "holds"], ["uncertainty_reflected", "warn", "the ranking is more certain than the open cost question allows"], ["no_blocked_preferred", "pass", "holds"], ["rationale_matches_basis", "pass", "holds"]], [`candidate:${a}`, act]),
      { ...MODEL, prompt_version: "ranking:v1" }),
    row("D3", "no_blocked_preferred", "", set, `ranking:${setId}`, "pass", `preferred ${a}: blocking=false restricted=false`, "the preferred candidate is neither blocked nor restricted", [`candidate:${a}`, `strategy_set:${setId}`], [], { kind: "deterministic" }),
    row("D4", "", "DIFFERENT_BUT_DEFENSIBLE", { type: "HumanStrategyDecision", id: e.decision.id }, `human_interaction:${e.episodeId}`, "pass",
      "the person chose the second option over the preferred one; ranking [a, b, c]", "the chosen candidate was not worse evaluated than the preferred one: a different but defensible preference", [`human_strategy_decision:${e.decision.id}`, `candidate:${b}`], [], { kind: "deterministic" }),
    row("D5", "", "", { type: "JudgmentInference", id: e.inference.id }, `human_interaction:${e.episodeId}`, "pass", "edit class cta; signal moderate; the person softened the ask",
      "the choice and edit were interpreted from cited evidence; the person's verdict can still correct it", [`human_strategy_decision:${e.decision.id}`, act], [], { ...MODEL, prompt_version: "judgment_inference:v1" }),
    row("D7", "", "", { type: "DecisionEpisode", id: e.episodeId }, `recomputed_action:${e.episodeId}`, "pass", "1 edit; 3 dependents invalidated; 3 recomputed before send", "all dependents were invalidated and recomputed before send; unrelated state is unchanged", [`candidate:${b}`], [], { kind: "deterministic" }),
    row("D8", "model", "", { type: "FinalArtifact", id: b }, `recomputed_action:${e.episodeId}`, "pass", "intent_preserved=pass, claims_grounded=pass, cta_timing_correct=pass", "every judged dimension passed with evidence", [`candidate:${b}`, act],
      dims([["intent_preserved", "pass", "holds"], ["claims_grounded", "pass", "holds"], ["cta_timing_correct", "pass", "holds"]], [`candidate:${b}`, act]),
      { ...MODEL, prompt_version: "final_artifact:v1" }),
  ];
}

/** The stored DecisionRanking of the MedTech episode (GET /episodes/{id}/ranking). */
export function medtechRanking(e) {
  const [a, b, c] = [...e.strategies.strategy_set.candidates].sort((x, y) => x.ranking - y.ranking).map((x) => x.candidate_id);
  const act = `activity:${e.trace.trigger_activities[0].id}`;
  return {
    episode_id: e.episodeId, strategy_set_id: e.strategies.strategy_set.id, order: [a, b, c], preferred_candidate_id: a,
    tier_inputs: [a, b, c].map((id, i) => ({ candidate_id: id, blocking: false, restricted: false, worker_rank: i + 1 })),
    reasons: [
      { ranked_higher_id: a, ranked_lower_id: b, reason: "Booking the call answers the buyer's request for a cost conversation directly, while writing the costs down first delays the one thing they asked for.", evidence_refs: [act], knowledge_refs: [] },
      { ranked_higher_id: b, ranked_lower_id: c, reason: "Putting the costs in writing stays within what the evidence supports; matching a competitor on onboarding claims parity nobody has shown.", evidence_refs: [act], knowledge_refs: [] },
    ],
    abstained: false, model: null,
  };
}
