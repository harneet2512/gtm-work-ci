// Measured eval quality for /evals, from a committed semantic-judge report (bench/evals/run_judges.py output,
// bench/reports/judges-*.json): per eval type, verdict agreement with gold, false-pass and false-block rates and
// the number of judgments. The report's own provenance (gold set, judge model, trials, date) travels with the
// numbers so the page can say exactly what was measured. Anything unreadable is "not measured", never a guess.
import { readFileSync } from "node:fs";
import path from "node:path";
import { formatDay } from "@/lib/format";
import type { EvalQuality } from "./registry";

/** The judge report the overview reads by default: gold v2, 3 trials (the newest per-eval measurement in the repo). */
export const DEFAULT_REPORT = "bench/reports/judges-2026-10-03-goldv2-deepseek-v4-flash-t3.json";

export interface QualitySource {
  model: string;
  gold: string;
  cases: number;
  trials: number;
  judged: number;
  generatedAt: string;
  agreement: number | null;
  kappa: number | null;
  falsePass: number | null;
  falseBlock: number | null;
  file: string;
}

export interface QualityReport {
  byType: Map<string, EvalQuality>;
  source: QualitySource;
}

interface Stats {
  n_judged?: number;
  insufficient_data?: boolean;
  verdict_agreement?: number | null;
  verdict_kappa?: number | null;
  false_pass_rate?: number | null;
  false_block_rate?: number | null;
}

const num = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) ? v : null);
/** "openrouter/deepseek/deepseek-v4-flash" -> "deepseek-v4-flash": the model, not the route. */
const modelName = (m: string): string => m.split("/").at(-1) ?? m;
/** "legacy fixture gold (…)" -> "Gold v2" when the file says so; else the report's own words. */
const goldLabel = (gold: string, file: string): string => (/goldv2/.test(file) ? "Gold v2" : gold);

export function parseQualityReport(doc: unknown, file: string): QualityReport | null {
  if (!doc || typeof doc !== "object") return null;
  const d = doc as { model?: unknown; gold?: unknown; n_cases?: unknown; trials?: unknown; generated_at?: unknown; overall?: Stats; per_eval?: Record<string, Stats> };
  if (typeof d.model !== "string" || typeof d.gold !== "string" || !d.per_eval || typeof d.per_eval !== "object" || typeof d.generated_at !== "string") return null;
  const model = modelName(d.model);
  const label = `${goldLabel(d.gold, file)} · ${model} · ${formatDay(d.generated_at)}`;
  const byType = new Map<string, EvalQuality>();
  for (const [type, s] of Object.entries(d.per_eval)) {
    const agreement = num(s.verdict_agreement);
    const cases = num(s.n_judged) ?? 0;
    if (s.insufficient_data || agreement === null || cases === 0) continue;
    byType.set(type, { agreement, falsePass: num(s.false_pass_rate), falseBlock: num(s.false_block_rate), cases, report: label });
  }
  const o = d.overall ?? {};
  return {
    byType,
    source: {
      model,
      gold: d.gold,
      cases: num(d.n_cases) ?? 0,
      trials: num(d.trials) ?? 0,
      judged: num(o.n_judged) ?? 0,
      generatedAt: d.generated_at,
      agreement: num(o.verdict_agreement),
      kappa: num(o.verdict_kappa),
      falsePass: num(o.false_pass_rate),
      falseBlock: num(o.false_block_rate),
      file: path.basename(file),
    },
  };
}

export function loadQualityReport(file: string): QualityReport | null {
  try {
    return parseQualityReport(JSON.parse(readFileSync(file, "utf8")), file);
  } catch {
    return null;
  }
}

export function qualityReportPathFromEnv(env: Readonly<Record<string, string | undefined>>, cwd: string): string {
  return env.GHOST_EVAL_QUALITY_REPORT?.trim() || path.resolve(cwd, "..", DEFAULT_REPORT);
}
