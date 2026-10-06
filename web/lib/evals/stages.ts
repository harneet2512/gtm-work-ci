// The three buckets and their gates (the registry's `buckets` and `gates`): Bucket 1 context / organizational
// intelligence (B1-B9), Bucket 2 decision / human judgment / action (D1-D10, in the order of the decision flow) and
// Bucket 3 system / eval health (S1-S5). Each gate says what question it asks and what it improves or protects; the evals
// under it are listed by name, never by code. Nothing here carries a result: the registry says what an eval judges and
// when, not how it did, so every gate and eval reads "Not measured" until a recorded result exists, a gate with no eval
// registered (the eval-gap and feedback gates today) reads "Not built yet", and no verdict is ever invented.

export type BucketId = "context_intelligence" | "decision_action" | "system_health";

/** What each judged object is, in a seller's words (the registry names the contract type). */
const JUDGED: Readonly<Record<string, string>> = {
  StrategySet: "the set of options",
  StrategyCandidate: "one option",
  DecisionRanking: "the order of the options",
  HumanStrategyDecision: "the person's choice",
  JudgmentInference: "gtm_ai's reading of why the person chose it",
  HumanDelta: "the person's edit",
  KnowledgeMutation: "a change to company knowledge",
  DependencyInvalidation: "what the edit made out of date",
  DecisionEpisode: "the whole episode",
  EvalResult: "the checks on the message that will be sent",
  DecisionGuidance: "the guidance built from company knowledge",
  KnowledgeUse: "how an option uses company knowledge",
  Knowledge: "a piece of company knowledge",
  AgentRun: "the company knowledge the run read",
  TraceSpan: "a step the run took",
};

/** Where in the demo an eval is exercised. */
const MOMENT: Readonly<Record<string, string>> = {
  M2: "When the three options appear",
  CES: "When the person chooses, edits or sends",
  M3: "When the person answers gtm_ai's reading",
  ECOLITE: "In the second account",
  OFFLINE: "Offline only",
  LATER: "Over later episodes",
};

export const judgedWords = (type: string | undefined): string | null => (type ? (JUDGED[type] ?? null) : null);
export const momentWords = (moments: readonly string[] | undefined): string[] => (moments ?? []).map((m) => MOMENT[m]).filter((m): m is string => Boolean(m));

export interface BucketSource {
  id: BucketId;
  order: number;
  name: string;
  question: string;
}

export interface GateSource {
  id: string;
  bucket: BucketId;
  order: number;
  name: string;
  question: string;
  improves: string;
}

/** The registry fields a gate reads from one eval. */
export interface GateEvalSource {
  name: string;
  status: string;
  grader: string;
  gate: string;
  judged_object?: { type: string };
  demo_moment?: readonly string[];
}

export interface GateEval {
  name: string;
  /** What it judges, in words; null when the registry names no object. */
  judges: string | null;
  when: string[];
  status: string;
  grader: string;
  /** Always "Not measured": the overview holds no recorded result, and a missing result is never shown as a pass. */
  result: "Not measured";
}

export interface GateView {
  id: string;
  order: number;
  name: string;
  /** What question did we ask? */
  question: string;
  /** What does this improve or protect? */
  improves: string;
  evals: GateEval[];
  /** False for a gate no eval is registered under yet: it reads "Not built yet". */
  built: boolean;
  result: "Not measured";
}

export interface BucketView {
  id: BucketId;
  order: number;
  name: string;
  question: string;
  gates: GateView[];
}

/** The buckets in order, each with its gates in order (Bucket 2's are the decision flow) and the evals under each gate. */
export function buildBuckets(buckets: readonly BucketSource[], gates: readonly GateSource[], evals: readonly GateEvalSource[]): BucketView[] {
  return [...buckets]
    .sort((a, b) => a.order - b.order)
    .map((b) => ({
      id: b.id,
      order: b.order,
      name: b.name,
      question: b.question,
      gates: gates
        .filter((g) => g.bucket === b.id && g.id !== "S6")
        .sort((x, y) => x.order - y.order)
        .map((g): GateView => {
          const under = evals
            .filter((e) => e.gate === g.id)
            .map((e): GateEval => ({ name: e.name, judges: judgedWords(e.judged_object?.type), when: momentWords(e.demo_moment), status: e.status, grader: e.grader, result: "Not measured" }));
          return { id: g.id, order: g.order, name: g.name, question: g.question, improves: g.improves, evals: under, built: under.length > 0, result: "Not measured" };
        }),
    }));
}
