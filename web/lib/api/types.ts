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
