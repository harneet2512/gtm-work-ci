// The landing's model (HAR-149 section 1): the three canonical buckets as human questions, the flow Bucket 1 -> Bucket 2 ->
// execution with feedback returning to Bucket 1, and the four modes as badges and filters (never buckets).
import { type GateDefinitionSource, MODES, type ModeKey, modeOf } from "@/lib/evals/gate-cards";
import { BUCKET_WORDS, type BucketWords } from "./buckets";

export interface LoopGate {
  id: string;
  /** The question in human words; the id is secondary. */
  question: string;
  mode: ModeKey;
  badge: string;
}

export interface LoopBucket extends Omit<BucketWords, "gates"> {
  gates: LoopGate[];
}

export interface LoopModel {
  buckets: LoopBucket[];
  flow: { label: string; note: string }[];
  feedback: string;
  modes: { id: ModeKey; badge: string; line: string; count: number }[];
}

const MODE_ORDER: readonly ModeKey[] = ["live_required", "live_conditional", "offline_benchmark", "continuous_aggregate"];

export function buildLoop(defs: readonly GateDefinitionSource[]): LoopModel {
  const byId = new Map(defs.map((d) => [d.id, d]));
  const buckets: LoopBucket[] = BUCKET_WORDS.map((b) => ({
    ...b,
    gates: b.gates.flatMap((id) => {
      const d = byId.get(id);
      if (!d) return [];
      const mode = modeOf(d.mode);
      return [{ id, question: d.display_question ?? d.question, mode, badge: MODES[mode].badge }];
    }),
  }));
  const all = buckets.flatMap((b) => b.gates);
  return {
    buckets,
    flow: [
      { label: "Bucket 1", note: "understand" },
      { label: "Bucket 2", note: "decide" },
      { label: "Execution", note: "act" },
    ],
    feedback: "What the human does and what the customer replies feeds back into Bucket 1, so the next decision starts from a better understanding.",
    modes: MODE_ORDER.map((id) => ({ id, badge: MODES[id].badge, line: MODES[id].line, count: all.filter((g) => g.mode === id).length })),
  };
}

/** Keeps only the gates of one mode (or all). The input is not changed; a bucket left with no gate stays, empty. */
export function filterGates(buckets: readonly LoopBucket[], mode: ModeKey | "all"): LoopBucket[] {
  return buckets.map((b) => ({ ...b, gates: mode === "all" ? [...b.gates] : b.gates.filter((g) => g.mode === mode) }));
}
