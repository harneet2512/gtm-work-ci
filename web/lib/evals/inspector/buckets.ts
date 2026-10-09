// The three canonical buckets (HAR-97 v2, HAR-149 section 1) as human questions. Live / Conditional / Offline / Continuous are
// badges and filters on a gate, never buckets. Ids are secondary.
export type BucketId = "context_intelligence" | "decision_action" | "system_health";

export interface BucketWords {
  id: BucketId;
  number: 1 | 2 | 3;
  prefix: "B" | "D" | "S";
  /** The headline: the question a person asks. */
  question: string;
  /** One calm line under it. */
  line: string;
  /** The gate ids, in order. */
  gates: readonly string[];
}

export const BUCKET_WORDS: readonly BucketWords[] = [
  {
    id: "context_intelligence",
    number: 1,
    prefix: "B",
    question: "Do we understand what is happening?",
    line: "What gtm_ai knows about the account now, and how the new event changed it.",
    gates: ["B1", "B2", "B3", "B4", "B5", "B6", "B7", "B8", "B9"],
  },
  {
    id: "decision_action",
    number: 2,
    prefix: "D",
    question: "Given what we know, are we doing the right thing?",
    line: "The three options, the recommendation, what the human changed, and whether the final action was carried out.",
    gates: ["D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8", "D9", "D10"],
  },
  {
    id: "system_health",
    number: 3,
    prefix: "S",
    question: "Can we trust the machinery measuring the first two?",
    line: "Whether the traces, the graders and the repeated runs deserve our trust, and what the system costs to run.",
    gates: ["S1", "S2", "S3", "S4", "S5", "S6"],
  },
];

export const bucketOf = (gate: string): BucketWords | null => BUCKET_WORDS.find((b) => b.prefix === gate.charAt(0)) ?? null;

/** The bucket as one line: "Bucket 2 · Given what we know, are we doing the right thing?" */
export const bucketLine = (gate: string): string => {
  const b = bucketOf(gate);
  return b ? `Bucket ${b.number} · ${b.question}` : "";
};
