// Measured judge quality for /evals, from a recorded snapshot of the semantic judges: per eval type, verdict agreement
// with reference verdicts, false-pass and false-block rates and the number of judgments. It is a recorded measurement,
// NOT a live reading, and the page says so with the date it was recorded. The benchmark's internals (reference set, judge
// model, trials, file name) are not carried to the page. Anything unreadable is "not measured", never a guess.
import { readFileSync } from "node:fs";
import path from "node:path";
import { formatDay } from "@/lib/format";
import type { EvalQuality } from "./registry";

/** The recorded snapshot the overview reads by default: a point-in-time measurement kept beside the repository, not live telemetry. */
const SNAPSHOT_DIRECTORY = ["bench", "reports"] as const;
export const DEFAULT_SNAPSHOT_FILE = "judges-2026-10-03-goldv2-deepseek-v4-flash-t3.json";

/**
 * The limits of the recorded snapshot, in plain words (no file or model names). It was measured on an earlier judge,
 * against reference verdicts written by one author, and has not been re-measured on the judge that runs today.
 */
export const JUDGE_QUALITY_CAVEAT =
  "These figures come from an earlier judge model, checked against reference verdicts written by one author. They have not been re-measured on the judge that runs today, so read them as a past measurement, not the current judge's score.";

/** What the page may say about the snapshot: when it was recorded and the pooled numbers. */
export interface QualitySource {
  /** When the snapshot was recorded (not when the page was rendered). */
  recordedAt: string;
  judged: number;
  agreement: number | null;
  falsePass: number | null;
  falseBlock: number | null;
}

export interface QualityReport {
  byType: Map<string, EvalQuality>;
  source: QualitySource;
}

interface Stats {
  n_judged?: number;
  insufficient_data?: boolean;
  verdict_agreement?: number | null;
  false_pass_rate?: number | null;
  false_block_rate?: number | null;
}

const num = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) ? v : null);

export function parseQualityReport(doc: unknown): QualityReport | null {
  if (!doc || typeof doc !== "object") return null;
  const d = doc as { generated_at?: unknown; overall?: Stats; per_eval?: Record<string, Stats> };
  if (!d.per_eval || typeof d.per_eval !== "object" || typeof d.generated_at !== "string") return null;
  const label = `Recorded ${formatDay(d.generated_at)}`;
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
    source: { recordedAt: d.generated_at, judged: num(o.n_judged) ?? 0, agreement: num(o.verdict_agreement), falsePass: num(o.false_pass_rate), falseBlock: num(o.false_block_rate) },
  };
}

export function loadQualityReport(file: string): QualityReport | null {
  try {
    return parseQualityReport(JSON.parse(readFileSync(file, "utf8")));
  } catch {
    return null;
  }
}

export function qualityReportPathFromEnv(env: Readonly<Record<string, string | undefined>>, cwd: string): string {
  return env.GHOST_EVAL_QUALITY_REPORT?.trim() || path.resolve(cwd, "..", ...SNAPSHOT_DIRECTORY, DEFAULT_SNAPSHOT_FILE);
}
