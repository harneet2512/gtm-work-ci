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
