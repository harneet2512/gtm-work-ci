// The edit recomputation view (HAR-145 / HAR-97 E11): for each of the human's edits, what it invalidated, what was
// re-evaluated at send time, what was NOT recomputed and what was preserved, exactly as the core derives it from the
// saved edits, the candidate's EvalBundle, the send-time batch and the account state the send read. The web infers
// nothing (the old client-side guess from before/after text is gone). Whether account state was preserved is shown as
// yes, no or not known; a state that is not proven preserved is never described as preserved.
import type { DependencyInvalidation } from "@/lib/api/types";
import { evalName } from "@/lib/evals/vocabulary";

type SpanRef = DependencyInvalidation["entries"][number]["invalidated"][number];

export interface RefView {
  kind: SpanRef["kind"];
  label: string;
  reason: string;
  /** "was pass" (the old verdict), "now pass" (the new one) or "still warn" (untouched); null when the ref carries none. */
  verdict: string | null;
  key: string;
}

export interface EntryView {
  index: number;
  edit: string;
  invalidated: RefView[];
  reevaluated: RefView[];
  notRecomputed: RefView[];
  preserved: RefView[];
}

export interface RecomputationView {
  headline: string;
  state: { tone: "yes" | "no" | "unknown"; text: string };
  labels: string[];
  entries: EntryView[];
  overallPreserved: RefView[];
}

const HEADLINE: Record<DependencyInvalidation["status"], string> = {
  not_decided: "No decision has been made yet, so nothing was edited.",
  unedited: "The chosen action was not edited.",
  edits_pending: "The edits are saved; the final action is re-evaluated when the human sends it.",
  reevaluated: "The final artifact was re-evaluated at send time. The ranking and the knowledge use are never recomputed.",
  discarded: "The action was discarded.",
  recomputation_unavailable: "What these edits recomputed could not be established.",
};

const VERDICT_PREFIX = { invalidated: "was", reevaluated: "now", notRecomputed: "was", preserved: "still" } as const;

type Bucket = keyof typeof VERDICT_PREFIX;

function refView(r: SpanRef, bucket: Bucket): RefView {
  return {
    kind: r.kind,
    label: r.kind === "eval_result" ? evalName(r.label) : r.label,
    reason: r.reason,
    verdict: r.verdict ? `${VERDICT_PREFIX[bucket]} ${r.verdict}` : null,
    key: `${r.kind}:${r.ref_id ?? "none"}:${r.field ?? ""}`,
  };
}

const humanize = (s: string) => s.replaceAll("_", " ");

function stateOf(s: DependencyInvalidation["account_state"]): RecomputationView["state"] {
  if (s.preserved === true) return { tone: "yes", text: `Account state preserved (v${s.version_before}, unchanged)` };
  if (s.preserved === false) return { tone: "no", text: `Account state was not preserved (v${s.version_before} → v${s.version_after})` };
  return { tone: "unknown", text: "Whether account state was preserved is not known" };
}

export function buildRecomputationView(d: DependencyInvalidation): RecomputationView {
  return {
    headline: HEADLINE[d.status],
    state: stateOf(d.account_state),
    labels: d.semantic_labels.map(humanize),
    entries: d.entries.map((e) => ({
      index: e.index,
      edit: `${e.edit.field}: ${humanize(e.edit.kind)}`,
      invalidated: e.invalidated.map((r) => refView(r, "invalidated")),
      reevaluated: e.recomputed.map((r) => refView(r, "reevaluated")),
      notRecomputed: e.not_recomputed.map((r) => refView(r, "notRecomputed")),
      preserved: e.preserved.map((r) => refView(r, "preserved")),
    })),
    overallPreserved: d.preserved_overall.map((r) => refView(r, "preserved")),
  };
}
