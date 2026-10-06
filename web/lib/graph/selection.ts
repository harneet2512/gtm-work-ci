// What the inspector shows for a graph node: the workspace's node selection (evidence activities), plus a
// fact's own field and value, its standing ("Outranked, retained" for a claim the winner beat), the quotes and
// speakers behind it, what the Play event did to it and what it used to be, and the nodes one hop away so the
// user can walk the graph from the inspector. A withheld node shows none of it.
import { formatDay } from "@/lib/format";
import { nodeSelection, type EvidenceLink, type RelatedNode, type Selection, type SelectionFact } from "@/lib/view/provenance";
import { factFor, type ClaimFact, type ClaimIndex } from "./claim-index";
import { standingLabel, type FieldHistory } from "./history";
import { fieldTitle, humanValue } from "./labels";
import type { ExplorerModel, ExplorerNode } from "./model";

function related(model: ExplorerModel, id: string): RelatedNode[] {
  const out: RelatedNode[] = [];
  for (const e of model.edges) {
    if (e.source !== id && e.target !== id) continue;
    const other = model.byId.get(e.source === id ? e.target : e.source)!;
    out.push({ id: other.id, name: other.name, rel: e.label, direction: e.source === id ? "out" : "in" });
  }
  return out.sort((a, b) => a.rel.localeCompare(b.rel) || a.name.localeCompare(b.name));
}

/** A fact value that is itself a node id (an owner, a champion) reads as that node's label. */
const readable = (model: ExplorerModel, text: string): string => humanValue(model.byId.get(text)?.label ?? text);

function claimRefs(fact: ClaimFact | undefined, claimId: string, nodeStanding: string | undefined): EvidenceLink[] {
  const raw = fact?.standing ?? nodeStanding;
  const standing = raw ? standingLabel(raw) : undefined;
  return (fact?.refs ?? []).map((r) => ({
    activityId: r.activity_id,
    claimId: r.claim_id ?? claimId,
    ...(r.quote ? { quote: r.quote } : {}),
    ...(r.speaker_person_id ? { speaker: r.speaker_person_id } : {}),
    ...(standing ? { standing } : {}),
  }));
}

function standingFact(fact: ClaimFact, nodeStanding: string | undefined): SelectionFact[] {
  if (fact.outranked) return [{ label: "Standing", value: "Outranked, retained" }];
  const raw = fact.standing ?? nodeStanding;
  return raw ? [{ label: "Standing", value: standingLabel(raw) }] : [];
}

function factsOf(model: ExplorerModel, node: ExplorerNode, fact: ClaimFact | undefined, history: FieldHistory): SelectionFact[] {
  const facts: SelectionFact[] = [];
  if (fact) {
    facts.push({ label: "Field", value: fieldTitle(fact.fieldPath) });
    if (fact.text !== "") facts.push({ label: "Value", value: readable(model, fact.text) });
    facts.push(...standingFact(fact, node.standing));
  }
  if (node.mark) facts.push({ label: "This event", value: node.mark });
  // Temporal memory: only for a fact this event touched, and only when its field really changed.
  const change = node.mark && fact ? history.get(fact.fieldPath) : undefined;
  if (change) {
    const until = change.changedAt ? ` (until ${formatDay(change.changedAt)})` : "";
    facts.push({ label: "Used to be", value: `${readable(model, change.before)}${until}` });
  }
  return facts;
}

export function selectionForNode(model: ExplorerModel, id: string, claims: ClaimIndex, history: FieldHistory): Selection | null {
  const node = model.byId.get(id);
  if (!node) return null;
  const base = nodeSelection({
    id: node.id,
    type: node.kindTag,
    label: node.label,
    evidence_refs: node.evidence.map((a) => ({ activity_id: a })),
    source_event_ids: [...node.sourceEventIds],
    ...(node.status ? { status: node.status } : {}),
    ...(node.validFrom ? { valid_from: node.validFrom } : {}),
    ...(node.withheld ? { withheld: "visibility" as const } : {}),
  });
  const meta = [node.kindTag, base.meta].filter(Boolean).join(" · ");
  const titled = { ...base, title: node.label, meta };
  if (node.withheld) return { ...titled, related: related(model, id) };
  const fact = node.type === "Claim" || node.type === "Commitment" ? factFor(claims, id, node.field) : undefined;
  const quoted = claimRefs(fact, id, node.standing);
  // The fact's own evidence (with quotes) first; any other activity the graph cites after it.
  const seen = new Set(quoted.map((r) => r.activityId));
  const refs = [...quoted, ...titled.refs.filter((r) => !seen.has(r.activityId))];
  return { ...titled, refs, facts: factsOf(model, node, fact, history), related: related(model, id) };
}

/** The inspector for something the event took out of the view: what it was called and what the event did to it. */
export function ghostSelection(model: ExplorerModel, id: string): Selection | null {
  const ghost = model.ghostById.get(id);
  if (!ghost) return null;
  return {
    kind: "node",
    id: ghost.id,
    title: ghost.label,
    meta: `${ghost.kindTag} · no longer in the graph`,
    withheld: false,
    refs: [],
    facts: [
      { label: "This event", value: ghost.op },
      { label: "What changed", value: ghost.detail },
    ],
    related: [],
  };
}
