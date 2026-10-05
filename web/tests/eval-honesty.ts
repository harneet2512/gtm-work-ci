// Shared honesty checks for fixture EvalResults: a result must agree with the eval catalog on its kind, its
// one evidence class, its label vocabulary and whether it may block.
import path from "node:path";
import type { EvalResult } from "@/lib/api/types";
import { CONTRACTS_DIR, readJson } from "./contract-validator";

interface CatalogType {
  kind: string;
  evidence_class: string;
  labels: string[];
  can_block: boolean;
}

export const catalog = readJson<{ eval_types: Record<string, CatalogType> }>(path.join(CONTRACTS_DIR, "evals", "eval_catalog.json")).eval_types;

/** Every disagreement between one result and the catalog, as readable strings. */
export function catalogIssues(r: EvalResult): string[] {
  const t = catalog[r.eval_type];
  if (!t) return [`${r.eval_type} is not in the catalog`];
  const issues: string[] = [];
  if (r.kind !== t.kind) issues.push(`${r.eval_type}: kind ${r.kind}, catalog ${t.kind}`);
  if (r.evidence_class !== t.evidence_class) issues.push(`${r.eval_type}: evidence class ${r.evidence_class}, catalog ${t.evidence_class}`);
  if (t.labels.length === 0 && r.label !== null) issues.push(`${r.eval_type}: label ${r.label}, catalog has none`);
  if (t.labels.length > 0 && (r.label === null || r.label === undefined || !t.labels.includes(r.label))) {
    issues.push(`${r.eval_type}: label ${r.label}, catalog ${t.labels.join("|")}`);
  }
  if (r.blocking && !t.can_block) issues.push(`${r.eval_type}: blocks, catalog says it cannot`);
  return issues;
}
