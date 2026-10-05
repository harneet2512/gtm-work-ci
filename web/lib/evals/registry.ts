// The eval overview's data: the eval catalog (the 35 checks that judge a drafted action), the eval registry (the evals of
// the strategy, E1-E22 and M1-M5) and the completeness matrix's surface names, read from contracts/ at request time.
// Turbopack cannot import outside web/, and the contracts stay the single source. What the audience sees is the WEB
// CATALOG: the contract less what is out of scope for the current demo (WEB_HIDDEN_EVALS), with the wording the web owns
// (INVARIANT_WORDING, SURFACE_WORDING). Judge quality (agreement, false pass, false block) comes only from a recorded
// judge snapshot; without one it says so.
import { readFileSync } from "node:fs";
import path from "node:path";
import type { QualitySource } from "./quality-report";
import { evalName, evalQuestion, evidenceTag, type EvidenceClass, type GraderKind, type Tag, WORDING } from "./vocabulary";

interface CatalogType {
  kind: GraderKind;
  evidence_class: EvidenceClass;
  can_block: boolean;
  description: string;
  blocking_rule?: string;
}

interface RegistryEval {
  id: string;
  family: string;
  name: string;
  surfaces: number[];
  status: "implemented" | "partial" | "planned";
  grader: "deterministic" | "model" | "hybrid" | null;
  mode: "blocking" | "shadow" | "advisory" | null;
  definitions_ref?: { catalog_types?: string[] };
}

interface RegistryFamily {
  id: string;
  name: string;
  class: "behavioral" | "execution" | "validation" | "safety" | "operational";
  job: EvalJob;
  invariant: string;
}

/**
 * The three eval jobs of the HAR-129 demo loop (contracts/schemas/eval_registry.v1.json `job`): intelligence-building
 * (context to trustworthy organizational intelligence), the decision and feedback loop, and system metrics (secondary).
 */
export type EvalJob = "intelligence" | "decision_loop" | "system";

export interface EvalContracts {
  catalog: { eval_types: Record<string, CatalogType> };
  registry: { families: RegistryFamily[]; evals: RegistryEval[] };
  matrix: { surfaces: { id: number; name: string }[] };
}

/**
 * Registry evals the web catalog leaves out: they belong to experiments that are not part of the current demo, so they are
 * neither listed nor counted. (The registry itself keeps them for internal validation.)
 */
export const WEB_HIDDEN_EVALS: ReadonlySet<string> = new Set(["E16.5", "E22.4"]);

/** Family invariants the web words itself: no claim about arms or comparisons the product does not make. */
const INVARIANT_WORDING: Readonly<Record<string, string>> = {
  E16: "Applicable learned knowledge shows up in later decisions, and knowledge that does not apply is left out.",
};

/** Surface names the web words itself, by completeness-matrix surface id. */
const SURFACE_WORDING: Readonly<Record<number, string>> = {
  18: "Continual learning and regression",
};

/** One eval type's measured quality from a recorded judge snapshot. */
export interface EvalQuality {
  agreement: number;
  /** Null when the report had no gold failure to miss (or no gold pass to block). */
  falsePass: number | null;
  falseBlock: number | null;
  /** Judgments compared with gold. */
  cases: number;
  report: string;
}

export interface CheckRow {
  evalType: string;
  name: string;
  question: string | null;
  definition: string;
  grader: string;
  canBlock: boolean;
  blockingRule: string | null;
  surfaces: string[];
  quality: EvalQuality | null;
}

export interface CheckGroup {
  evidenceClass: EvidenceClass;
  tag: Tag;
  rows: CheckRow[];
}

export interface StrategyEval {
  id: string;
  name: string;
  status: string;
  grader: string;
  mode: string;
  surfaces: string[];
}

export interface Family {
  id: string;
  name: string;
  job: EvalJob;
  kind: string;
  invariant: string;
  evals: StrategyEval[];
  counts: { live: number; partial: number; planned: number };
}

export interface Overview {
  groups: CheckGroup[];
  families: Family[];
  totals: { draftTypes: number; registryEvals: number; live: number; partial: number; planned: number };
  measured: boolean;
  /** When the recorded numbers were measured; null when nothing is measured. */
  source: QualitySource | null;
}

export function contractsDirFromEnv(env: Readonly<Record<string, string | undefined>>, cwd: string): string {
  return env.GHOST_CONTRACTS_DIR?.trim() || path.resolve(cwd, "..", "contracts");
}

export function loadEvalContracts(dir: string): EvalContracts {
  const read = (file: string) => JSON.parse(readFileSync(path.join(dir, "evals", file), "utf8"));
  try {
    return { catalog: read("eval_catalog.json"), registry: read("eval_registry.json"), matrix: read("completeness_matrix.json") };
  } catch (cause) {
    throw new Error(`eval contracts could not be read from ${dir}`, { cause });
  }
}

const sentence = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

const INTERNAL_REFERENCE = /HAR-\d+|ADR-\d+|§/;

/**
 * The contract's own description, less the parenthetical pointers to tickets, decision records and strategy sections
 * ("(HAR-97 ask_strength)", "(ADR-0012)", "(§5.4, ...)"): they help an engineer and mean nothing to the audience.
 */
export function publicText(text: string): string {
  return text
    .replace(/\s*\((?:[^()]|\([^()]*\))*\)/g, (group) => (INTERNAL_REFERENCE.test(group) ? "" : group))
    .replace(/\s+([.,;])/g, "$1")
    .trim();
}

const STATUS: Readonly<Record<RegistryEval["status"], string>> = { implemented: "Live", partial: "Partial", planned: "Planned" };
const GRADER: Readonly<Record<string, string>> = { deterministic: WORDING.graders.deterministic, model: WORDING.graders.semantic, hybrid: "Rule check + AI judge" };
const MODE: Readonly<Record<string, string>> = { blocking: "Blocks", shadow: "Shadow: recorded, not acted on", advisory: "Advisory" };
const KIND: Readonly<Record<RegistryFamily["class"], string>> = { behavioral: "Behavior", execution: "Execution", validation: "Validation", safety: "Safety", operational: "Operations" };
/** Most proven first: a hard rule, then the deal's own data, then methodology, then CS practice. */
const CLASS_ORDER: readonly EvidenceClass[] = ["product_rule", "deal_data", "methodology", "cs_ops"];

export function buildOverview(c: EvalContracts, quality: ReadonlyMap<string, EvalQuality> | null, source: QualitySource | null = null): Overview {
  const surfaceName = new Map(c.matrix.surfaces.map((s) => [s.id, SURFACE_WORDING[s.id] ?? sentence(s.name)]));
  const visibleEvals = c.registry.evals.filter((e) => !WEB_HIDDEN_EVALS.has(e.id));
  const names = (ids: readonly number[]) => [...new Set(ids.map((id) => surfaceName.get(id) ?? `Surface ${id}`))];
  const surfacesOf = new Map<string, number[]>();
  for (const e of visibleEvals) for (const t of e.definitions_ref?.catalog_types ?? []) surfacesOf.set(t, [...(surfacesOf.get(t) ?? []), ...e.surfaces]);

  const rows = Object.entries(c.catalog.eval_types).map(([evalType, t]): CheckRow & { cls: EvidenceClass } => ({
    cls: t.evidence_class,
    evalType,
    name: evalName(evalType),
    question: evalQuestion(evalType),
    definition: publicText(t.description),
    grader: WORDING.graders[t.kind],
    canBlock: t.can_block,
    blockingRule: t.blocking_rule ? publicText(t.blocking_rule) : null,
    surfaces: names(surfacesOf.get(evalType) ?? []),
    quality: quality?.get(evalType) ?? null,
  }));
  const groups = CLASS_ORDER.map((cls) => ({
    evidenceClass: cls,
    tag: evidenceTag(cls),
    rows: rows
      .filter((r) => r.cls === cls)
      .map(({ cls: _cls, ...row }) => row)
      .sort((a, b) => a.name.localeCompare(b.name)),
  }));

  const families = c.registry.families.map((f): Family => {
    const evals = visibleEvals.filter((e) => e.family === f.id);
    return {
      id: f.id,
      name: f.name,
      job: f.job,
      kind: KIND[f.class],
      invariant: INVARIANT_WORDING[f.id] ?? f.invariant,
      evals: evals.map((e) => ({
        id: e.id,
        name: sentence(e.name),
        status: STATUS[e.status],
        grader: e.grader ? (GRADER[e.grader] ?? e.grader) : "Not built yet",
        mode: e.mode ? (MODE[e.mode] ?? e.mode) : "Not built yet",
        surfaces: names(e.surfaces),
      })),
      counts: {
        live: evals.filter((e) => e.status === "implemented").length,
        partial: evals.filter((e) => e.status === "partial").length,
        planned: evals.filter((e) => e.status === "planned").length,
      },
    };
  });

  const all = visibleEvals;
  return {
    groups,
    families,
    totals: {
      draftTypes: rows.length,
      registryEvals: all.length,
      live: all.filter((e) => e.status === "implemented").length,
      partial: all.filter((e) => e.status === "partial").length,
      planned: all.filter((e) => e.status === "planned").length,
    },
    measured: (quality?.size ?? 0) > 0,
    source: (quality?.size ?? 0) > 0 ? source : null,
  };
}
