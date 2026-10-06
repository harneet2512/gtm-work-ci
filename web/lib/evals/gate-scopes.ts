// HAR-145: the two views of the gates that are never mixed. The episode view answers "What protected this decision?" with
// only that episode's live and conditional gates, in causal order; System Proof answers "How do we know the eval system is
// trustworthy?" with S2-S5 and the offline coverage of each B and D gate. A live PASS and "not yet calibrated" are separate
// rows. Every count or date shown is one the backend exposes; none is exposed today, so each reads "not measured".
import type { GateCard } from "./gate-cards";

/** HAR-145's causal order: INGEST B1-B4 B7? B8, DECIDE D1-D3, HUMAN D4 D5? D6? D7?, SEND D8 D9 D10?. */
export const EPISODE_ORDER: readonly { phase: "INGEST" | "DECIDE" | "HUMAN" | "SEND"; gates: readonly string[] }[] = [
  { phase: "INGEST", gates: ["B1", "B2", "B3", "B4", "B7", "B8"] },
  { phase: "DECIDE", gates: ["D1", "D2", "D3"] },
  { phase: "HUMAN", gates: ["D4", "D5", "D6", "D7"] },
  { phase: "SEND", gates: ["D8", "D9", "D10"] },
];

export interface ProtectionGate {
  id: string;
  card: GateCard;
  /** Marked with "?": runs only when its trigger exists. */
  conditional: boolean;
  /** Why a conditional gate did not run when its trigger is absent ("no human edit"). */
  skipReason: string | null;
}

export interface ProtectionPhase {
  phase: "INGEST" | "DECIDE" | "HUMAN" | "SEND";
  gates: ProtectionGate[];
}

export function episodeProtection(cards: readonly GateCard[]): ProtectionPhase[] {
  const byId = new Map(cards.map((c) => [c.id, c]));
  return EPISODE_ORDER.map((p) => ({
    phase: p.phase,
    gates: p.gates.flatMap((id): ProtectionGate[] => {
      const card = byId.get(id);
      if (!card || (card.mode !== "live_required" && card.mode !== "live_conditional")) return [];
      return [{ id, card, conditional: card.mode === "live_conditional", skipReason: card.mode === "live_conditional" ? card.notTriggered : null }];
    }),
  }));
}

export interface CoverageRow {
  id: string;
  name: string;
  /** Offline gold or regression cases for the gate; the backend exposes none, so "not measured". */
  cases: "not measured";
  lastRun: "not measured";
}

export interface SystemProof {
  /** S2 Eval Trust, S3, S4 and S5. */
  trust: GateCard[];
  coverage: CoverageRow[];
  /** Model graders are calibrated against reference answers by a stronger model, pending. */
  calibration: "not yet calibrated";
}

const rank = (id: string): number => (id[0] === "B" ? 0 : 100) + Number(id.slice(1));

export function systemProof(cards: readonly GateCard[]): SystemProof {
  return {
    trust: cards.filter((c) => ["S2", "S3", "S4", "S5"].includes(c.id)).sort((a, b) => rank(a.id) - rank(b.id)),
    coverage: cards
      .filter((c) => c.id[0] === "B" || c.id[0] === "D")
      .sort((a, b) => rank(a.id) - rank(b.id))
      .map((c) => ({ id: c.id, name: c.name, cases: "not measured", lastRun: "not measured" })),
    calibration: "not yet calibrated",
  };
}
