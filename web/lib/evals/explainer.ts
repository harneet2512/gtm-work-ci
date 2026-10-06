// The Explaining evals page's model (HAR-97 eval list, HAR-149): the human copy lives in contracts/evals/explainer.v1.json,
// keyed by the registry's gate id. The ids are keys only: they order and join the cards and are never rendered. An eval the
// registry does not list yet is a card marked "coming soon". The contract is read at request time, like the registry.
import { readFileSync } from "node:fs";
import path from "node:path";

export type MomentId = "m1" | "m2" | "m3" | "next_case" | "health";
type Cadence = "every_time" | "when_triggered" | "offline" | "ongoing";
type DecidesBy = "rule" | "judge" | "both" | "measurement";

interface Label { label: string; meaning: string }

export interface ExplainerEntry {
  id: string;
  moment: MomentId;
  cadence: Cadence;
  human_name: string;
  what: string;
  why: string;
  example: string;
  when: string;
  result_effect: string;
  decides_by: DecidesBy;
}

export interface Explainer {
  intro: { title: string; lead: string; why_heading: string; why: string; results_heading: string; results: Label[]; honesty_note: string };
  cadence_labels: Record<Cadence, string>;
  decides_by_labels: Record<DecidesBy, Label>;
  moments: { id: MomentId; kicker: string; title: string; question: string; intro: string }[];
  entries: ExplainerEntry[];
}

export interface ExplainerCard {
  /** The registry gate id: a join key for tests, never rendered. */
  key: string;
  humanName: string;
  what: string;
  why: string;
  example: string;
  when: string;
  resultEffect: string;
  decidesBy: Label;
  cadence: string;
  comingSoon: boolean;
}

export interface ExplainerSection {
  id: MomentId;
  kicker: string;
  title: string;
  question: string;
  intro: string;
  cards: ExplainerCard[];
}

export interface ExplainerPage {
  intro: Explainer["intro"];
  sections: ExplainerSection[];
}

export function loadExplainer(contractsDir: string): Explainer {
  try {
    return JSON.parse(readFileSync(path.join(contractsDir, "evals", "explainer.v1.json"), "utf8")) as Explainer;
  } catch (cause) {
    throw new Error(`the eval explainer could not be read from ${contractsDir}`, { cause });
  }
}

/** Sections in the order of the product loop; cards in story order inside each. `registryGates` is the set of gate ids the registry lists. */
export function buildExplainerPage(explainer: Explainer, registryGates: ReadonlySet<string>): ExplainerPage {
  const sections = explainer.moments.map((m): ExplainerSection => ({
    ...m,
    cards: explainer.entries
      .filter((e) => e.moment === m.id)
      .map((e): ExplainerCard => ({
        key: e.id,
        humanName: e.human_name,
        what: e.what,
        why: e.why,
        example: e.example,
        when: e.when,
        resultEffect: e.result_effect,
        decidesBy: explainer.decides_by_labels[e.decides_by],
        cadence: explainer.cadence_labels[e.cadence],
        comingSoon: !registryGates.has(e.id),
      })),
  }));
  return { intro: explainer.intro, sections };
}
