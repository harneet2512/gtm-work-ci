// The four area health bands of /control (HAR-145). Each band is one area of the account's latest EvalRun with its real
// pass / warn / fail / unknown counts; the tone is the worst real verdict and never a score. An area with no results
// reads "not measured", a missing run "no eval run yet", an unreachable backend "backend unavailable" (never a FAIL).
// Deltas compare with the account's previous episode, which is a different event, not a re-run of the same trigger.
import type { EpisodeSummary, EvalRun } from "@/lib/api/types";

export type AreaId = EvalRun["areas"][number]["area"];
type Counts = EvalRun["counts"];
type CountsDelta = NonNullable<EvalRun["areas"][number]["delta"]>;

export interface HealthBand {
  id: AreaId;
  label: string;
  /** One-line status, honest about "not measured". */
  status: string;
  /**
   * `ok`/`warn`/`fail` come only from real EvalResult verdicts; `unsure` is an abstain (gtm_ai could not decide, which is
   * not a warning); `recorded` is a backend fact (a message posted); `none` is "no signal".
   */
  tone: "ok" | "warn" | "fail" | "unsure" | "recorded" | "none";
  facts: string[];
}

export interface BandContext {
  /** Posted Cliff message kinds; null when the surface refs could not be read. */
  cliffKinds: readonly string[] | null;
  /** The latest decision episode's summary, when one could be read. */
  episode: EpisodeSummary | null;
  /** The replay world's own facts (state version, knowledge in scope), shown under Intelligence. */
  intelligenceFacts: readonly string[];
  /** The eval-run read failed for a reason other than "none exist". */
  unavailable: boolean;
}

// "✓ healthy" is reserved for a tone that comes from real EvalResult verdicts.
export const TONE_MARK: Record<HealthBand["tone"], string> = { ok: "✓", warn: "!", fail: "✗", unsure: "◌", recorded: "●", none: "·" };
export const TONE_WORD: Record<HealthBand["tone"], string> = { ok: "healthy", warn: "warning", fail: "failing", unsure: "unsure", recorded: "recorded", none: "no signal" };

const AREAS: readonly { id: AreaId; label: string }[] = [
  { id: "intelligence", label: "Intelligence" },
  { id: "decision_learning", label: "Decision & Learning" },
  { id: "cliff_experience", label: "Cliff / Experience" },
  { id: "system", label: "System" },
];

const MESSAGE: Record<string, string> = { bi: "Message 1", chooser: "Message 2", judgment: "Message 3" };

export function toneOf(c: Counts): HealthBand["tone"] {
  if (c.fail > 0) return "fail";
  if (c.warn > 0) return "warn";
  if (c.unknown > 0) return "unsure";
  return c.pass > 0 ? "ok" : "none";
}

const countsLine = (c: Counts): string => `${c.pass} pass · ${c.warn} warn · ${c.fail} fail · ${c.unknown} unknown`;

const signed = (n: number): string => (n > 0 ? `+${n}` : String(n));

/** What moved against the previous episode (a different event): only the counts that changed. */
export function deltaLine(d: CountsDelta | null): string {
  if (!d) return "no previous episode to compare";
  const moved = (["pass", "warn", "fail", "unknown"] as const).filter((k) => d[k] !== 0).map((k) => `${signed(d[k])} ${k}`);
  return `vs previous episode: ${moved.length > 0 ? moved.join(" · ") : "no change"}`;
}

const emptyBand = (id: AreaId, label: string, status: string, facts: string[] = []): HealthBand => ({ id, label, status, tone: "none", facts });

function contextFacts(id: AreaId, ctx: BandContext): string[] {
  if (id === "intelligence") return [...ctx.intelligenceFacts];
  if (id === "decision_learning") return ctx.episode ? [`episode: ${ctx.episode.final_status.replaceAll("_", " ")}`] : [];
  if (id === "cliff_experience") return ctx.cliffKinds === null ? ["Slack posts not observable"] : ctx.cliffKinds.map((k) => `${MESSAGE[k] ?? k} posted`);
  return [];
}

function measuredBand(id: AreaId, label: string, a: EvalRun["areas"][number], ctx: BandContext): HealthBand {
  const facts = [countsLine(a.counts)];
  if (a.counts.blocking_fail > 0) facts.push(`${a.counts.blocking_fail} blocking`);
  facts.push(deltaLine(a.delta));
  return { id, label, status: `${a.counts.total} ${a.counts.total === 1 ? "result" : "results"}`, tone: toneOf(a.counts), facts: [...facts, ...contextFacts(id, ctx)] };
}

function unmeasuredBand(id: AreaId, label: string, ctx: BandContext): HealthBand {
  const facts = contextFacts(id, ctx);
  const posted = id === "cliff_experience" && (ctx.cliffKinds?.length ?? 0) > 0;
  return { id, label, status: "not measured", tone: posted ? "recorded" : "none", facts };
}

/** One band per area, in the product order. `evalRun` is the account's newest run with results, or null. */
export function buildBands(evalRun: EvalRun | null, ctx: BandContext): HealthBand[] {
  if (!evalRun) {
    const status = ctx.unavailable ? "backend unavailable" : "no eval run yet";
    return AREAS.map((a) => emptyBand(a.id, a.label, status, contextFacts(a.id, ctx)));
  }
  return AREAS.map(({ id, label }) => {
    const a = evalRun.areas.find((x) => x.area === id);
    return a?.measured ? measuredBand(id, label, a, ctx) : unmeasuredBand(id, label, ctx);
  });
}
