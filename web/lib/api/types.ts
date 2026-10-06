// Types come from the OpenAPI contract (npm run gen:api -> schema.d.ts). Nothing here is hand-written
// beyond aliases, so a contract change surfaces as a type error.
import type { components, operations } from "./schema";

type Schemas = components["schemas"];

export type Graph = Schemas["Graph"];
export type GraphNode = Graph["nodes"][number];
export type GraphEdge = Graph["edges"][number];
export type GraphDiff = Schemas["GraphDiff"];
export type GraphChange = GraphDiff["changes"][number];
export type AccountSummary = Schemas["AccountSummary"];
export type AccountState = Schemas["account_state.v1"];
export type AgentRun = Schemas["agent_run.v1"];
export type Activity = Schemas["activity.v1"];
export type OpenTransition = NonNullable<AccountState["open_transition"]>;
export type RelationshipState = NonNullable<AccountState["relationship_state"]>;
export type StateField = Schemas["scalarField"] | Schemas["listField"] | Schemas["derivedField"];
export type EvidenceRef = Schemas["evidenceRef"];

type JsonOf<Op extends keyof operations, Status extends number> = operations[Op] extends {
  responses: Record<Status, { content: { "application/json": infer T } }>;
}
  ? T
  : never;

export type Timeline = JsonOf<"getTimeline", 200>;

// Episode replay (HAR-129 section B/C): the computed view, one sequence event, and the results of
// advancing and resetting the released cursor.
export type EpisodeReplayView = Schemas["episode_replay.v1"];
export type EpisodeEvent = Schemas["episodeEvent"];
export type EpisodeState = Schemas["episodeState"];
export type EpisodeKnowledge = Schemas["episodeKnowledge"];
export type KnowledgeItem = Schemas["knowledgeItem"];
export type AdvanceResult = Schemas["advanceResult"];
export type ResetResult = Schemas["resetResult"];

// WP24 (HAR-122): the decision chain a run detail page renders — strategies + evals + the human's
// decision + the trace + the semantic delta inference + learned knowledge.
export type Signal = Schemas["signal.v1"];
export type StateDiff = Schemas["state_diff.v1"];
export type TriggerEvaluation = Schemas["trigger_evaluation.v1"];
export type ContextPacket = Schemas["context_packet.v1"];
export type HumanDecision = Schemas["human_decision.v1"];
export type Knowledge = Schemas["knowledge.v1"];
export type KnowledgeStatus = Knowledge["status"];
export type KnowledgeCondition = Knowledge["situation_signature"][number];
export type RunTrace = Schemas["RunTrace"];
export type RunStrategies = Schemas["RunStrategies"];
export type StrategySet = Schemas["strategy_set.v1"];
export type StrategyCandidate = Schemas["strategy_candidate.v1"];
export type EvalBundle = Schemas["eval_bundle.v1"];
export type EvalBundleItem = EvalBundle["items"][number];
export type EvalResult = Schemas["eval_result.v1"];
export type HumanStrategyDecision = Schemas["human_strategy_decision.v1"];
export type JudgmentInference = Schemas["judgment_inference.v1"];
export type LiteralChange = Schemas["literal_changes-items"];
export type FinishedArtifact = Schemas["finished_artifact"];
export type Recipient = Schemas["items"];

// Eval disputes (HAR-97 E19): "This eval is wrong" on one EvalResult.
export type EvalDispute = Schemas["eval_dispute.v1"];
export type EvalDisputeRequest = Schemas["EvalDisputeRequest"];

// HAR-136 surface-message refs (a posted Cliff message's existence proof).
export type SurfaceMessage = Schemas["surface_message.v1"];
export type BusinessIntelligence = Schemas["business_intelligence_update.v1"];

// System view (HAR-145): the unacked outbox queue, the provider breaker and the held-out leak check.
export type OutboxEvent = Schemas["outbox_event.v1"];
export type ProviderBreakerStatus = Schemas["ProviderBreakerStatus"];
export type InvisibilityReport = Schemas["InvisibilityReport"];

// HAR-145 control-plane reads: live Play progress, the episode summary and trace, what an edit recomputed,
// knowledge mutations, operational metrics and the eval-run roll-ups. METRIC, EVAL and TRACE stay separate types.
/** The generated type of a schema that declares its own $defs carries them as a required property; the documents never do. */
type NoDefs<T> = Omit<T, "$defs">;

export type PipelineProgress = NoDefs<Schemas["pipeline_progress.v1"]>;
export type PipelineStageRow = PipelineProgress["stages"][number];
export type EpisodeSummary = NoDefs<Schemas["episode_summary.v1"]>;
export type EpisodeTrace = NoDefs<Schemas["episode_trace.v1"]>;
export type TraceSpan = EpisodeTrace["spans"][number];
export type KnowledgeMutation = NoDefs<Schemas["knowledge_mutation.v1"]>;
/** One stored gate result of an episode (B1-B9, D1-D10, S1-S5; gate_result.v1.json). */
export type GateResult = NoDefs<Schemas["gate_result.v1"]>;
/** HAR-149: one per-criterion result of a gate result. */
export type GateCriterion = NonNullable<GateResult["criteria"]>[number];
/** HAR-149: the stored DecisionRanking of an episode (order, tier inputs and a reason per adjacent pair). */
export type EpisodeRanking = JsonOf<"getEpisodeRanking", 200>;
export type OperationalMetrics = NoDefs<Schemas["operational_metrics.v1"]>;
export type EvalRun = NoDefs<Schemas["eval_run.v1"]>;
export type EvalFamilySummary = NoDefs<Schemas["eval_family_summary.v1"]>;
export type EvalRunComparison = NoDefs<Schemas["eval_run_comparison.v1"]>;
export type DependencyInvalidation = NoDefs<Schemas["dependency_invalidation.v1"]>;

/** A keyset page: `nextCursor` is null on the last page. */
export interface Page<T> {
  items: T[];
  nextCursor: string | null;
}
