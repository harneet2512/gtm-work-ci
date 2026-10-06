// Claim values and their quotes are not on the graph (it carries ids, labels, status and evidence refs only):
// they come from the account state, where every known field and list item names the claim it came from.
// One claim can win several fields (a summary and a risk read from the same sentence), so a claim maps to
// every fact it fills and a caller picks the fact by field: a node never shows another field's value.
import type { AccountState, EvidenceRef, StateField } from "@/lib/api/types";
import { formatValue } from "@/lib/format";

export interface ClaimFact {
  claimId: string;
  fieldPath: string;
  /** The value the claim asserts, as a reader would say it; empty for an outranked claim (the state keeps only the winner's value). */
  text: string;
  refs: readonly EvidenceRef[];
  standing: string | null;
  confidence: number | null;
  /** When the field last changed. */
  asOf: string | null;
  /** A competing claim the winner outranked: kept on record, not shown as the field's value. */
  outranked: boolean;
}

export type ClaimIndex = ReadonlyMap<string, readonly ClaimFact[]>;

interface ListItem {
  text: string;
  claim_id: string;
  evidence_refs?: EvidenceRef[];
}

const isListItem = (v: unknown): v is ListItem =>
  typeof v === "object" && v !== null && typeof (v as ListItem).text === "string" && typeof (v as ListItem).claim_id === "string";

type Field = StateField;

function factsOfField(fieldPath: string, field: Field): ClaimFact[] {
  const base = { fieldPath, standing: field.standing ?? null, confidence: field.confidence ?? null, asOf: field.as_of ?? null, outranked: false };
  const out: ClaimFact[] = [];
  if (Array.isArray(field.value)) {
    for (const item of field.value as unknown[]) {
      if (isListItem(item)) out.push({ ...base, claimId: item.claim_id, text: item.text, refs: item.evidence_refs ?? [] });
    }
  }
  const winner = field.winning_claim_id;
  if (!Array.isArray(field.value) && typeof winner === "string" && winner !== "") {
    out.push({ ...base, claimId: winner, text: formatValue(field.value), refs: field.evidence_refs.filter((r: EvidenceRef) => !r.claim_id || r.claim_id === winner) });
  }
  for (const loser of field.competing_claim_ids ?? []) {
    if (loser !== winner) out.push({ ...base, claimId: loser, text: "", refs: [], standing: null, confidence: null, outranked: true });
  }
  return out;
}

/** Claim id -> every fact it fills (field, value, evidence, standing). */
export function indexClaims(state: AccountState | null): ClaimIndex {
  const out = new Map<string, ClaimFact[]>();
  if (!state) return out;
  for (const [fieldPath, field] of Object.entries(state.fields)) {
    for (const fact of factsOfField(fieldPath, field)) out.set(fact.claimId, [...(out.get(fact.claimId) ?? []), fact]);
  }
  return out;
}

/**
 * The fact of `claimId` for `field` (the core labels a Claim node with its field), and nothing when the claim
 * does not fill that field: a node never borrows another field's value. Without a field, the claim's first fact.
 */
export function factFor(index: ClaimIndex, claimId: string, field?: string): ClaimFact | undefined {
  const facts = index.get(claimId) ?? [];
  return field === undefined ? facts[0] : facts.find((f) => f.fieldPath === field);
}
