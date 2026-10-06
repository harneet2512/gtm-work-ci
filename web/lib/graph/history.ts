// Temporal memory: what each account-state field was just before the Play event and what it is now, read
// from the two world-time state reads (strictly before N, and inclusive of N). A fact the event changed can then
// say what it used to be, and since when it is different. Also the standing of a fact, in words.
import type { AccountState, StateField } from "@/lib/api/types";
import { formatValue } from "@/lib/format";
import { humanizeKey } from "./labels";

export interface FieldChange {
  before: string;
  after: string;
  /** When the field took its new value (the field's as_of, else the state's). */
  changedAt: string | null;
}

export type FieldHistory = ReadonlyMap<string, FieldChange>;

const NOT_KNOWN = "not known";

const valueText = (field: StateField | undefined): string => (!field || !field.known ? NOT_KNOWN : formatValue(field.value));

/** Every field whose value differs between the state before the event and the state with it. */
export function fieldHistory(before: AccountState | null, now: AccountState | null): FieldHistory {
  const out = new Map<string, FieldChange>();
  if (!before || !now) return out;
  for (const [path, field] of Object.entries(now.fields)) {
    const was = valueText((before.fields as Readonly<Record<string, StateField>>)[path]);
    const is = valueText(field);
    if (was !== is) out.set(path, { before: was, after: is, changedAt: field.as_of ?? now.as_of ?? null });
  }
  return out;
}

const STANDINGS: Readonly<Record<string, string>> = {
  human_approved: "Approved by a person",
  crm_explicit: "CRM record",
  first_party_record: "First-party record",
  first_party_ai: "AI reading of first-party evidence",
  third_party: "Third-party source",
};

/** A standing (contracts/schemas/common.v1.json) in words. */
export const standingLabel = (standing: string): string => STANDINGS[standing] ?? humanizeKey(standing);
