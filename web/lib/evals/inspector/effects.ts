// The control effect of a gate result (HAR-149 section 7): what the verdict did in the backend. Derived only from the
// backend: the effect stored with the result, else the registry's control_effect_rules (cited from code), plus the real
// lineage (a result that replaced an earlier one after an edit is RECOMPUTE, one that replaced a failed attempt RETRY).
// Most gates only RECORD ONLY: nothing here may imply a hard stop the code does not make.
import type { GateResult } from "@/lib/api/types";

export type ControlEffect = "OBSERVE" | "CONTINUE" | "RANK" | "RECOMPUTE" | "RETRY" | "BLOCK" | "ESCALATE" | "MARK UNKNOWN" | "RECORD ONLY";
type Verdict = "pass" | "warn" | "fail" | "unknown";

export interface EffectRules {
  vocabulary: readonly string[];
  description: string;
  default: Record<Verdict, ControlEffect>;
  gates: Record<string, Partial<Record<Verdict, ControlEffect>> & { basis?: string; only_for?: { grader: string; sub_gates: readonly string[] } }>;
}

export const EFFECT_WORDS: Readonly<Record<ControlEffect, { label: string; plain: string }>> = {
  OBSERVE: { label: "Observe", plain: "Watched and shown; it does not change what gtm_ai does." },
  CONTINUE: { label: "Continue", plain: "The work carries on to the next step." },
  RANK: { label: "Rank", plain: "The result changes the order the options are shown in." },
  RECOMPUTE: { label: "Recompute", plain: "The check ran again because something it depends on changed." },
  RETRY: { label: "Retry", plain: "The check ran again after an earlier attempt could not be used." },
  BLOCK: { label: "Block", plain: "A failure here stops the action it guards." },
  ESCALATE: { label: "Escalate", plain: "A failure here goes to a person to decide." },
  "MARK UNKNOWN": { label: "Mark unknown", plain: "The result says gtm_ai could not tell, and it is shown as not measured, never as a pass." },
  "RECORD ONLY": { label: "Record only", plain: "Recorded and shown. Nothing in the backend reads this result to change the decision." },
};

const VERDICTS: readonly Verdict[] = ["pass", "warn", "fail", "unknown"];
const readVerdict = (v: string): Verdict => ((VERDICTS as readonly string[]).includes(v) ? (v as Verdict) : "unknown");

/** The effect the registry's rules give a verdict of a gate; with no rules, unknown marks unknown and anything else records. */
export function effectForVerdict(rules: EffectRules | null, gate: string, verdict: string): ControlEffect {
  const v = readVerdict(verdict);
  // An override that holds only for some results (a deterministic D8 sub-gate) cannot be applied without knowing the result: it is not guessed.
  if (rules) return (rules.gates[gate]?.only_for ? undefined : rules.gates[gate]?.[v]) ?? rules.default[v];
  return v === "unknown" ? "MARK UNKNOWN" : "RECORD ONLY";
}

export interface EffectView {
  /** What the verdict itself did. */
  effect: ControlEffect;
  /** What leads the display: the lineage effect when the result is a re-run, else `effect`. */
  headline: ControlEffect;
  label: string;
  plain: string;
  /** True only for BLOCK. */
  hardStop: boolean;
  /** The code citation, from the rules for this gate, else the gate's impact_basis; null when neither says. */
  basis: string | null;
  lineage: { kind: "RECOMPUTE" | "RETRY"; previousVerdict: string | null } | null;
}

interface Source {
  gate: string;
  verdict: string;
  control_effect?: string;
  lineage?: { retry_of?: string; recompute_of?: string; previous_verdict?: string };
}

const isEffect = (s: string | undefined): s is ControlEffect => s !== undefined && s in EFFECT_WORDS;

export function controlEffectView(r: Source | GateResult, gate: { impact?: string; impact_basis?: string } | null, rules: EffectRules | null): EffectView {
  const src = r as Source;
  const effect = isEffect(src.control_effect) ? src.control_effect : effectForVerdict(rules, src.gate, src.verdict);
  const l = src.lineage ?? {};
  const lineage = l.retry_of ? { kind: "RETRY" as const, previousVerdict: l.previous_verdict ?? null } : l.recompute_of ? { kind: "RECOMPUTE" as const, previousVerdict: l.previous_verdict ?? null } : null;
  const headline = lineage ? lineage.kind : effect;
  return {
    effect,
    headline,
    label: EFFECT_WORDS[headline].label,
    plain: EFFECT_WORDS[headline].plain,
    hardStop: effect === "BLOCK",
    basis: rules?.gates[src.gate]?.basis ?? gate?.impact_basis ?? null,
    lineage,
  };
}
