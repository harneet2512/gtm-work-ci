// The registry as the inspector reads it (HAR-149): the 25 gate definitions with their plain words, the control effect rules and the
// owner deviations, from contracts/evals/eval_registry.json. Read at request time; the contracts stay the single source.
import type { GateDefinitionSource } from "@/lib/evals/gate-cards";
import { contractsDirFromEnv, loadEvalContracts } from "@/lib/evals/registry";
import type { EffectRules } from "./effects";
import type { Deviation } from "./offline";

export interface InspectorRegistry {
  defs: GateDefinitionSource[];
  rules: EffectRules | null;
  deviations: Deviation[];
}

export function loadInspectorRegistry(env: Readonly<Record<string, string | undefined>> = process.env, cwd: string = process.cwd()): InspectorRegistry {
  const registry = loadEvalContracts(contractsDirFromEnv(env, cwd)).registry as unknown as {
    gates: GateDefinitionSource[];
    control_effect_rules?: EffectRules;
    known_deviations?: Deviation[];
  };
  return { defs: registry.gates, rules: registry.control_effect_rules ?? null, deviations: registry.known_deviations ?? [] };
}
