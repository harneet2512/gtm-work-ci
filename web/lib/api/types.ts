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
