// The evals explorer and the operator's run comparison (HAR-145). Everything here is a tally the core served: counts of
// pass / warn / fail / unknown results per area, family and eval type, with no percentages and no scores. An area with
// no results reads "not measured"; deltas compare with the account's previous episode (a different event), not with a
// re-run of the same trigger. The run comparison is the before/after of ONE trigger and is an operator-only view: it is
// never offered in Demo mode.
import type { EvalFamilySummary, EvalRun, EvalRunComparison } from "@/lib/api/types";
import { evalName } from "@/lib/evals/vocabulary";
import { deltaLine, toneOf, type HealthBand } from "@/lib/view/bands";

type Counts = EvalRun["counts"];

const countsLine = (c: Counts): string => `${c.pass} pass · ${c.warn} warn · ${c.fail} fail · ${c.unknown} unknown`;

export interface AreaChip {
  id: EvalRun["areas"][number]["area"];
  label: string;
  measured: boolean;
  /** "not measured", or the counts. */
  text: string;
  tone: HealthBand["tone"];
}

export interface EvalRunRow {
  id: string;
  account: string;
  evaluatedAt: string;
  episodeId: string | null;
  total: string;
  areas: AreaChip[];
  previousId: string | null;
}

export function evalRunRow(run: EvalRun): EvalRunRow {
  return {
    id: run.id,
    account: run.account_name,
    evaluatedAt: run.evaluated_at,
    episodeId: run.decision_episode_id,
    total: `${run.result_count} ${run.result_count === 1 ? "result" : "results"}`,
    areas: [...run.areas]
      .sort((a, b) => a.order - b.order)
      .map((a) => ({ id: a.area, label: a.label, measured: a.measured, text: a.measured ? countsLine(a.counts) : "not measured", tone: a.measured ? toneOf(a.counts) : "none" })),
    previousId: run.previous_eval_run_id,
  };
}

export interface EvalTypeView {
  evalType: string;
  name: string;
  counts: string;
  delta: string;
  results: number;
}

export interface FamilyView {
  id: string;
  name: string;
  counts: string;
  delta: string;
  evalTypes: EvalTypeView[];
}

export interface AreaView {
  id: EvalRun["areas"][number]["area"];
  label: string;
  measured: boolean;
  status: string;
  tone: HealthBand["tone"];
  /** null when the area has no results: it is not measured, not healthy. */
  counts: string | null;
  blocking: string | null;
  delta: string | null;
  families: FamilyView[];
}

export function areaViews(summary: EvalFamilySummary): AreaView[] {
  return [...summary.areas]
    .sort((a, b) => a.order - b.order)
    .map((a) => {
      if (!a.measured) return { id: a.area, label: a.label, measured: false, status: "not measured", tone: "none", counts: null, blocking: null, delta: null, families: [] };
      return {
        id: a.area,
        label: a.label,
        measured: true,
        status: `${a.counts.total} ${a.counts.total === 1 ? "result" : "results"}`,
        tone: toneOf(a.counts),
        counts: countsLine(a.counts),
        blocking: a.counts.blocking_fail > 0 ? `${a.counts.blocking_fail} blocking` : null,
        delta: deltaLine(a.delta),
        families: a.families.map((f) => ({
          id: f.family_id,
          name: f.name,
          counts: countsLine(f.counts),
          delta: deltaLine(f.delta),
          evalTypes: f.eval_types.map((t) => ({ evalType: t.eval_type, name: evalName(t.eval_type), counts: countsLine(t.counts), delta: deltaLine(t.delta), results: t.result_ids.length })),
        })),
      };
    });
}

type Change = EvalRunComparison["rows"][number]["change"];

const CHANGE_WORD: Record<Change, string> = {
  improved: "improved",
  regressed: "regressed",
  unchanged: "unchanged",
  added: "checked only in run B",
  removed: "checked only in run A",
  inconclusive: "inconclusive",
};

const verdictWord = (v: EvalRunComparison["rows"][number]["a"]): string => v ?? "not checked";

export interface CompareRowView {
  evalType: string;
  name: string;
  a: string;
  b: string;
  aBlocking: boolean;
  bBlocking: boolean;
  change: Change;
  changeWord: string;
}

export interface CompareView {
  a: { runId: string; account: string; evaluatedAt: string; counts: string };
  b: { runId: string; account: string; evaluatedAt: string; counts: string };
  rows: CompareRowView[];
  overall: { change: EvalRunComparison["overall"]["change"]; word: string; counts: string; explanation: string | null };
}

const side = (s: EvalRunComparison["a"]) => ({ runId: s.eval_run_id, account: s.account_name, evaluatedAt: s.evaluated_at, counts: countsLine(s.counts) });

export function compareView(c: EvalRunComparison): CompareView {
  const o = c.overall;
  const parts = [`${o.improved} improved`, `${o.regressed} regressed`, `${o.unchanged} unchanged`, `${o.added} added`, `${o.removed} removed`, `${o.inconclusive} inconclusive`];
  if (o.added_fail > 0) parts.push(`${o.added_fail} added ${o.added_fail === 1 ? "failure" : "failures"}`);
  return {
    a: side(c.a),
    b: side(c.b),
    rows: c.rows.map((r) => ({
      evalType: r.eval_type,
      name: evalName(r.eval_type),
      a: verdictWord(r.a),
      b: verdictWord(r.b),
      aBlocking: r.a_blocking,
      bBlocking: r.b_blocking,
      change: r.change,
      changeWord: CHANGE_WORD[r.change],
    })),
    overall: {
      change: o.change,
      word: CHANGE_WORD[o.change],
      counts: parts.join(" · "),
      explanation: o.change === "inconclusive" ? "Some verdicts could not be judged either way, so the whole-episode change cannot be called an improvement or a regression." : null,
    },
  };
}

export type EvalsView = "catalog" | "results" | "compare";

const TABS: readonly { id: EvalsView; label: string }[] = [
  { id: "catalog", label: "Checks" },
  { id: "results", label: "Results" },
  { id: "compare", label: "Run comparison (operator)" },
];

/** The run comparison is operator-only: Demo mode never lists it. */
export const evalsTabs = (demo: boolean): readonly { id: EvalsView; label: string }[] => TABS.filter((t) => !(demo && t.id === "compare"));

export function resolveEvalsView(raw: string | undefined, demo: boolean): EvalsView {
  if (raw === "results") return "results";
  if (raw === "compare") return demo ? "results" : "compare";
  return "catalog";
}
