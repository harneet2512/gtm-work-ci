// "What went into it?" (HAR-149 section 3, part 4): a result's evidence refs resolved to readable content. The email quote is the
// activity's recorded summary, an option is named by its title, a ref that resolves to nothing stays a plain label. Raw JSON is
// never here: it lives under "Advanced".
import type { EvidenceItem } from "@/lib/evals/gate-table";

export type InputKind = "source" | "option" | "knowledge" | "human" | "state" | "ranking" | "edit" | "trace" | "other";

export interface InputItem {
  kind: InputKind;
  kindLabel?: string;
  title: string;
  /** The real text (a quote, a rationale); null when it could not be read. */
  text: string | null;
  /** Where the text comes from, in words ("Email · Nov 9, 2023"). */
  source: string | null;
  href: string | null;
}

const KIND_LABEL: Readonly<Record<InputKind, string>> = {
  source: "From the source",
  option: "An option",
  knowledge: "Company knowledge",
  human: "The human's action",
  state: "Account state",
  ranking: "The stored ranking",
  edit: "The human's edit",
  trace: "A step of the episode",
  other: "A record",
};

export const inputKindLabel = (k: InputKind): string => KIND_LABEL[k];

function kindOf(ref: string): InputKind {
  const k = ref.slice(0, ref.indexOf(":"));
  if (k === "activity" || k === "claim" || k === "source_event") return "source";
  if (k === "candidate" || k === "strategy_candidate" || k === "strategy_set") return "option";
  if (k === "knowledge" || k === "knowledge_evidence") return "knowledge";
  if (k === "human_strategy_decision" || k === "human_decision" || k === "human_delta" || k === "judgment_inference") return "human";
  if (k === "account_state" || k === "state_diff" || k === "account_change" || k === "graph_diff") return "state";
  if (k === "agent_run_step" || k === "span") return "trace";
  return "other";
}

export function inputFromEvidence(e: EvidenceItem, titleOf: (id: string) => string | null): InputItem {
  const kind = kindOf(e.ref);
  const named = titleOf(e.id);
  const title = kind === "source" && e.source ? e.source : named ?? e.label;
  return { kind, kindLabel: KIND_LABEL[kind], title, text: e.summary, source: e.source ?? null, href: e.href };
}
