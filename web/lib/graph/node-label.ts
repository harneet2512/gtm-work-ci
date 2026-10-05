// The label one graph node shows, from the reads the page already has: a fact's value from the account state
// (picked by the node's own field), an activity's people and day from the timeline, a signal's key in words.
import type { Activity, GraphNode } from "@/lib/api/types";
import { formatDay } from "@/lib/format";
import { factFor, type ClaimFact, type ClaimIndex } from "./claim-index";
import { isActivityType } from "./kinds";
import { activityTitle, fieldTitle, humanizeKey, humanValue } from "./labels";

export const WITHHELD_LABEL = "withheld by visibility";

export interface NodeLabelContext {
  claims: ClaimIndex;
  activities: ReadonlyMap<string, Activity>;
  /** The core's label of every node by id: a fact whose value is a node id (an owner) reads as that node. */
  labelsById: ReadonlyMap<string, string>;
}

export interface NodeLabel {
  label: string;
  /** The standing the core records on the fact node. */
  standing?: string;
  /** The fact the node stands for (by its own field), when the state has it. */
  fact?: ClaimFact;
  /** The field a fact node fills. */
  field?: string;
}

const isFact = (type: string): boolean => type === "Claim" || type === "Commitment";

/** A Claim node's field: the core puts it in `data.field_path` and in the label; a commitment has neither. */
function fieldOf(node: GraphNode): string | undefined {
  const fromData = (node.data as Record<string, unknown> | undefined)?.field_path;
  if (typeof fromData === "string" && fromData !== "") return fromData;
  return node.type === "Claim" ? node.label : undefined;
}

function standingOf(node: GraphNode): string | undefined {
  const s = (node.data as Record<string, unknown> | undefined)?.standing;
  return typeof s === "string" && s !== "" ? s : undefined;
}

function factLabel(node: GraphNode, ctx: NodeLabelContext): NodeLabel {
  const field = fieldOf(node);
  const fact = factFor(ctx.claims, node.id, field);
  const shownField = field ?? fact?.fieldPath;
  const standing = standingOf(node);
  const extra = { ...(fact ? { fact } : {}), ...(shownField ? { field: shownField } : {}), ...(standing ? { standing } : {}) };
  const title = shownField ? fieldTitle(shownField) : humanizeKey(node.label);
  if (!fact || fact.text === "") return { label: title, ...extra };
  return { label: `${title}: ${humanValue(ctx.labelsById.get(fact.text) ?? fact.text)}`, ...extra };
}

function activityLabel(node: GraphNode, ctx: NodeLabelContext): string {
  const activity = ctx.activities.get(node.id);
  const type = activity?.activity_type ?? node.label;
  const when = node.valid_from ?? activity?.occurred_at;
  const title = activityTitle(type, activity);
  return when && !Number.isNaN(Date.parse(when)) ? `${title} · ${formatDay(when)}` : title;
}

/** What a node is called on the canvas, in lists and in the inspector. */
export function nodeLabel(node: GraphNode, ctx: NodeLabelContext): NodeLabel {
  if (node.withheld === "visibility") return { label: WITHHELD_LABEL };
  if (isFact(node.type)) return factLabel(node, ctx);
  if (isActivityType(node.type)) return { label: activityLabel(node, ctx) };
  if (node.type === "Signal" || node.type === "DecisionEpisode") return { label: humanizeKey(node.label) };
  // People, the account, the deal and knowledge already carry names; a bare key is put in words.
  return { label: /^[a-z]+(_[a-z]+)+$/.test(node.label) ? humanizeKey(node.label) : node.label };
}
