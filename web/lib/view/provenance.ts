import type { Activity, EvidenceRef, GraphEdge, GraphNode, StateField } from "@/lib/api/types";
import { formatUtc, formatValue } from "@/lib/format";
import { standingLabel } from "@/lib/graph/history";
import { fieldTitle, humanValue } from "@/lib/graph/labels";

export interface EvidenceLink {
  activityId: string;
  claimId?: string;
  quote?: string;
  speaker?: string;
  /** The standing of the fact this evidence supports, in words. */
  standing?: string;
}

/** A labelled fact about the selection (a claim's value, what the Play event did to it). */
export interface SelectionFact {
  label: string;
  value: string;
}

/** A node one hop away in the graph, and how it is connected. */
export interface RelatedNode {
  id: string;
  name: string;
  rel: string;
  /** "out": this node -> the related one; "in": the related one -> this node. */
  direction: "in" | "out";
}

/** What the user clicked: a graph node, a graph edge, or a claim behind an account-state field. */
export interface Selection {
  kind: "node" | "edge" | "claim";
  id: string;
  title: string;
  meta: string;
  withheld: boolean;
  refs: readonly EvidenceLink[];
  facts?: readonly SelectionFact[];
  related?: readonly RelatedNode[];
}

export interface ProvenanceItem extends EvidenceLink {
  /** Undefined when the activity is outside the loaded timeline window. */
  activity?: Activity;
}

export interface Provenance {
  key: string;
  title: string;
  meta: string;
  withheld: boolean;
  items: ProvenanceItem[];
  facts: readonly SelectionFact[];
  related: readonly RelatedNode[];
}

const join = (parts: (string | null | undefined)[]): string => parts.filter(Boolean).join(" · ");

export function nodeSelection(node: GraphNode): Selection {
  const refs = node.evidence_refs.map((r) => ({ activityId: r.activity_id }));
  // An activity node is its own evidence even if the projection carried no explicit ref.
  const own = node.type === "Activity" && refs.length === 0 ? [{ activityId: node.id }] : refs;
  return {
    kind: "node",
    id: node.id,
    title: `${node.type}: ${node.label}`,
    refs: own,
    meta: join([node.status, node.valid_from ? `since ${formatUtc(node.valid_from)}` : undefined]),
    withheld: node.withheld === "visibility",
  };
}

export function edgeSelection(edge: GraphEdge, sourceLabel: string, targetLabel: string): Selection {
  return {
    kind: "edge",
    id: edge.id,
    title: `${edge.rel_type}: ${sourceLabel} -> ${targetLabel}`,
    refs: edge.evidence_refs.map((r) => ({ activityId: r.activity_id })),
    meta: join([edge.standing, edge.confidence === undefined ? undefined : `confidence ${edge.confidence}`]),
    withheld: false,
  };
}

function toLink(ref: EvidenceRef): EvidenceLink {
  return { activityId: ref.activity_id, claimId: ref.claim_id, quote: ref.quote, speaker: ref.speaker_person_id ?? undefined };
}

export function claimSelection(fieldPath: string, field: StateField): Selection {
  return {
    kind: "claim",
    id: `claim:${fieldPath}`,
    title: `${fieldTitle(fieldPath)}: ${humanValue(formatValue(field.value))}`,
    refs: field.evidence_refs.map((r) => ({ ...toLink(r), ...(field.standing ? { standing: standingLabel(field.standing) } : {}) })),
    meta: join([field.standing ? standingLabel(field.standing) : null, `confidence ${field.confidence}`, field.as_of ? `as of ${formatUtc(field.as_of)}` : null]),
    withheld: false,
  };
}

/** Turns a selection into the evidence the panel shows, resolving activity ids against the loaded timeline. */
export function resolveProvenance(selection: Selection, activities: ReadonlyMap<string, Activity>): Provenance {
  const items = selection.withheld ? [] : selection.refs.map((ref) => ({ ...ref, activity: activities.get(ref.activityId) }));
  return {
    key: selection.id,
    title: selection.title,
    meta: selection.meta,
    withheld: selection.withheld,
    items,
    facts: selection.facts ?? [],
    related: selection.related ?? [],
  };
}
