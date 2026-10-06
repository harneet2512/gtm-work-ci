// The node kinds of the account graph explorer: the colour family a kind belongs to, the shape it is drawn
// with, its size and whether its label is always shown (major) or only once the user zooms in (minor).
// Kinds are the node labels of contracts/graph/ontology.v1.json; anything else lands in "Other".

export type Shape = "ring" | "hexagon" | "circle" | "diamond" | "square" | "triangle" | "pentagon";

export type RegionId = "account" | "people" | "activity" | "knows" | "knowledge" | "signals" | "other";

export interface KindSpec {
  region: RegionId;
  shape: Shape;
  /** Radius in world units at zoom 1. */
  radius: number;
  /** Major kinds are always labelled; minor ones (activities, claims) only when zoomed in. */
  major: boolean;
  /** Label priority when labels collide: higher wins. */
  rank: number;
}

const KINDS: Readonly<Record<string, KindSpec>> = {
  Account: { region: "account", shape: "ring", radius: 12, major: true, rank: 9 },
  Opportunity: { region: "account", shape: "hexagon", radius: 10, major: true, rank: 8 },
  Person: { region: "people", shape: "circle", radius: 8, major: true, rank: 7 },
  Signal: { region: "signals", shape: "triangle", radius: 7, major: true, rank: 6 },
  Knowledge: { region: "knowledge", shape: "pentagon", radius: 7, major: true, rank: 6 },
  DecisionEpisode: { region: "knowledge", shape: "square", radius: 6, major: true, rank: 5 },
  Claim: { region: "knows", shape: "square", radius: 5.5, major: false, rank: 4 },
  Commitment: { region: "knows", shape: "square", radius: 5.5, major: false, rank: 4 },
  Activity: { region: "activity", shape: "diamond", radius: 5, major: false, rank: 3 },
  Conversation: { region: "activity", shape: "diamond", radius: 5, major: false, rank: 3 },
  Document: { region: "activity", shape: "diamond", radius: 5, major: false, rank: 3 },
};

const FALLBACK: KindSpec = { region: "other", shape: "circle", radius: 4.5, major: false, rank: 1 };

export const kindOf = (type: string): KindSpec => KINDS[type] ?? FALLBACK;

/** Kinds that are themselves a point in time: the arrow keys step through these. */
export const isActivityType = (type: string): boolean => kindOf(type).region === "activity";

// Retrieval is not influence: the agent reading a knowledge item is worded as a read, never as "used".
const EDGE_WORDS: Readonly<Record<string, string>> = { USED_KNOWLEDGE: "retrieved" };

/** "WORKS_AT" reads "works at". */
export function edgeLabel(relType: string): string {
  if (relType === "") return "related";
  return EDGE_WORDS[relType] ?? relType.toLowerCase().replaceAll("_", " ");
}
