// HAR-145 display naming (gtm_ai): the UI words for backend concepts, the six display statuses and the Cliff message
// chips. Display copy only; routes, backend fields and contracts keep their names. The gate -> message mapping is
// registry data (eval_registry.json `message`), never decided here.
import type { GateVerdict } from "./gate-table";

export const NAMING = {
  episode: "Judgment Episode",
  decisionLearning: "Judgment Loop",
  humanSelection: "Human Judgment",
  humanDelta: "Judgment Delta",
  evalGap: "Eval Gap",
  knowledgeMutation: "Judgment Learning",
  candidateCriterion: "Candidate Judgment Rule",
  recompute: "Judgment Recompute",
  s2: "Eval Trust",
} as const;

/** The D6 outcome labels (the stored class stays COVERED etc.). */
export const GAP_LABEL: Readonly<Record<string, string>> = {
  COVERED: "Eval caught it",
  MISGRADED: "Eval judged it wrong",
  MISSING_CRITERION: "Missing judgment criterion",
  HUMAN_PREFERENCE_ONLY: "Preference only",
};

/** Null stays null (nothing to say); any class we do not know reads Unknown, never a guess. */
export function gapLabel(raw: string | null | undefined): string | null {
  if (raw === null || raw === undefined) return null;
  return GAP_LABEL[raw] ?? "Unknown";
}

export interface Area {
  label: string;
  /** The Cliff message(s) of the one loop this area is. */
  message: string | null;
  /** The short rail label; the full title stays as its tooltip. */
  short?: string;
}

/** The control-plane areas: one loop, explained by its three Cliff messages. Cliff is the action surface, not an area of its own. */
export const AREAS: readonly Area[] = [
  { label: "Organizational Intelligence", message: "Message 1", short: "Intelligence · M1" },
  { label: "Judgment Loop", message: "Messages 2–3", short: "Judgment Loop · M2–M3" },
  { label: "Cliff messages · M1–M3", message: null },
  { label: "System Trust", message: null },
];

/** "Organizational Intelligence · Message 1": the area with its message, or just the area when it has none. */
export const areaShort = (a: Area): string => a.short ?? areaTitle(a);
export const areaTitle = (a: Area): string => (a.message ? `${a.label} · ${a.message}` : a.label);

export type DisplayStatus = "pass" | "warn" | "fail" | "unknown" | "not_run" | "not_applicable";
export const STATUS_ORDER: readonly DisplayStatus[] = ["pass", "warn", "fail", "unknown", "not_run", "not_applicable"];
const WORD: Readonly<Record<DisplayStatus, string>> = { pass: "PASS", warn: "WARN", fail: "FAIL", unknown: "UNKNOWN", not_run: "NOT RUN", not_applicable: "NOT APPLICABLE" };
export const statusWord = (s: DisplayStatus): string => WORD[s];

/** A required gate with no result is NOT RUN; a conditional gate whose trigger is absent is NOT APPLICABLE. Neither is a pass. */
export function displayStatus(verdict: GateVerdict | null, notApplicable: boolean): DisplayStatus {
  if (verdict !== null) return verdict;
  return notApplicable ? "not_applicable" : "not_run";
}

export type MessageId = "M1" | "M2" | "M3" | "ecolite" | "system";
export interface MessageInfo {
  id: MessageId;
  chip: string;
  /** The section header of the Gates view. */
  title: string;
  /** What this message is, in one line. */
  line: string;
}

/** The loop in order: M1 -> M2 -> M3, back to M1 on the next case through EcoLite Play; System Trust below. */
export const MESSAGES: readonly MessageInfo[] = [
  { id: "M1", chip: "M1", title: "Message 1 · What changed", line: "Organizational Intelligence: what the organization knows now and how this event changed it." },
  { id: "M2", chip: "M2", title: "Message 2 · What to do", line: "The decision: candidates, ranking, the human's choice, edit and send." },
  { id: "M3", chip: "M3", title: "Message 3 · What we learned from you", line: "Judgment Learning: what the human's correction means and what it changes." },
  { id: "ecolite", chip: "EcoLite Play", title: "EcoLite Play · Used next time", line: "Knowledge and decision guidance applied to the next case, which comes back as Message 1." },
  { id: "system", chip: "System Trust", title: "System Trust", line: "Can we trust the machinery behind the loop?" },
];

export const messageOf = (raw: string | undefined | null): MessageInfo | null => MESSAGES.find((m) => m.id === raw) ?? null;
