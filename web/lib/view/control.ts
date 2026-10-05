// The /control operator view (HAR-145): four product-area health bands, the recent material changes,
// and the episode trajectory — all pure derivations over the replay view, the account's latest run and
// its eval bundles. A band that has no data says so; nothing is fabricated.
import type { AgentRun, EpisodeReplayView, HumanStrategyDecision, RunStrategies } from "@/lib/api/types";
import type { VerdictCounts } from "@/lib/view/run-chain";

export type AreaId = "intelligence" | "decision_learning" | "cliff_experience" | "system";

export interface HealthBand {
  id: AreaId;
  label: string;
  /** One-line status for the area, honest about "no data". */
  status: string;
  /**
   * Worst finding tone for the band's marker. `ok`/`warn`/`fail` come only from real EvalResult verdicts; `unsure` is an
   * abstain (Ghost could not decide, which is not a warning); `recorded` is a backend fact (a message posted, steps done).
   */
  tone: "ok" | "warn" | "fail" | "unsure" | "recorded" | "none";
  /** The one or two facts the band rests on. */
  facts: string[];
}

export interface MaterialChange {
  position: number;
  label: string;
  detail: string | null;
  /** Set when the episode opened a decision. */
  decisionEpisodeId: string | null;
}

export interface TrajectoryStep {
  position: number;
  released: boolean;
  material: boolean | null;
  heldOut: boolean;
  label: string;
}

export interface ControlView {
  accountId: string;
  opportunityId: string | null;
  manifestId: string;
  accountName: string | null;
  /** Released episode k of N and which replay window k falls in. */
  episode: number;
  total: number;
  window: string;
  computedAt: string;
  bands: HealthBand[];
  changes: MaterialChange[];
  trajectory: TrajectoryStep[];
  canPlayNext: boolean;
  canPrevious: boolean;
  /** The next unreleased event's metadata; content stays withheld by contract. */
  nextEvent: { position: number; occurredAt: string; source: string; provenance: string | null } | null;
  stateVersion: number | null;
  stateDigest: string | null;
  stateAsOf: string | null;
  knowledgeCount: number;
  /** The account's latest run, when one exists — the bands' decision/system source. */
  run: AgentRun | null;
  /** Live-poll handle for the strip after Play: the advance result's decision episode. */
  decisionEpisodeId: string | null;
}

const MATERIAL_LABEL: Record<string, string> = {
  true: "changed the world",
};

const countVerdicts = (strategies: RunStrategies | null): VerdictCounts | null => {
  if (!strategies) return null;
  const c: VerdictCounts = { pass: 0, warn: 0, fail: 0, abstain: 0, notRelevant: 0 };
  for (const b of strategies.eval_bundles) {
    for (const it of b.items) {
      if (it.verdict === "pass") c.pass++;
      else if (it.verdict === "warn") c.warn++;
      else if (it.verdict === "fail") c.fail++;
      else if (it.verdict === "abstain") c.abstain++;
      else c.notRelevant++;
    }
  }
  return c;
};

const worstTone = (c: VerdictCounts | null): HealthBand["tone"] => {
  if (!c) return "none";
  if (c.fail > 0) return "fail";
  if (c.warn > 0) return "warn";
  if (c.abstain > 0) return "unsure";
  return c.pass > 0 ? "ok" : "none"; // only not_relevant (or no) verdicts: nothing was actually checked
};

const intelligenceBand = (view: EpisodeReplayView): HealthBand => {
  const facts: string[] = [];
  const materialCount = view.prior_episodes.filter((e) => e.material).length;
  facts.push(`${materialCount} material event${materialCount === 1 ? "" : "s"} released of ${view.episode}`);
  if (view.state) facts.push(`account state v${view.state.version} as of ${view.state.as_of ?? "?"}`);
  const applicable = view.knowledge.items.length;
  if (applicable > 0) facts.push(`${applicable} knowledge item${applicable === 1 ? "" : "s"} in scope`);
  // No intelligence EvalResult is read on this page, so the band never claims health: a material event
  // being released says the world changed, not that the agent understood it correctly.
  facts.push("intelligence evals not observable here");
  return { id: "intelligence", label: "Intelligence", status: "the world the agent saw", tone: "none", facts };
};

const decisionBand = (run: AgentRun | null, strategies: RunStrategies | null, decision: HumanStrategyDecision | null, unavailable: boolean): HealthBand => {
  if (!run && unavailable)
    return { id: "decision_learning", label: "Decision & Learning", status: "backend unavailable", tone: "none", facts: ["The core could not be reached, so no run is shown."] };
  if (!run) return { id: "decision_learning", label: "Decision & Learning", status: "no agent run yet", tone: "none", facts: ["Play a material event to open a decision episode."] };
  const c = countVerdicts(strategies);
  const facts: string[] = [];
  if (c) facts.push(`${c.fail} fail · ${c.warn} warn · ${c.pass} pass across ${strategies!.eval_bundles.length} options`);
  if (decision) facts.push(`human decision: ${decision.selected_candidate_id ? "chose an option" : decision.send_decision ?? "recorded"}`);
  const phase = run.generation?.phase;
  return { id: "decision_learning", label: "Decision & Learning", status: phase ? `run ${phase}` : "run recorded", tone: worstTone(c), facts };
};

const CLIFF_LABEL: Record<string, string> = { bi: "Message 1", chooser: "Message 2", judgment: "Message 3" };

const cliffBand = (cliffKinds: readonly string[] | null, decisionEpisodeId: string | null): HealthBand => {
  if (cliffKinds === null) return { id: "cliff_experience", label: "Cliff / Experience", status: "not observable", tone: "none", facts: ["Surface refs unreadable on this core."] };
  const posted = cliffKinds.length;
  if (posted === 0) return { id: "cliff_experience", label: "Cliff / Experience", status: decisionEpisodeId ? "nothing sent yet" : "waiting on a decision", tone: "none", facts: [] };
  return {
    id: "cliff_experience",
    label: "Cliff / Experience",
    status: `${posted} message${posted === 1 ? "" : "s"} posted`,
    tone: "recorded",
    facts: cliffKinds.map((k) => CLIFF_LABEL[k] ?? k),
  };
};

const systemBand = (run: AgentRun | null, unavailable: boolean): HealthBand => {
  if (!run && unavailable) return { id: "system", label: "System", status: "backend unavailable", tone: "none", facts: ["The core could not be reached."] };
  if (!run) return { id: "system", label: "System", status: "idle", tone: "none", facts: ["No run to measure."] };
  const steps = run.steps ?? [];
  const failed = steps.filter((s) => s.status === "failed").length;
  const done = steps.filter((s) => s.status === "succeeded" || s.status === "recorded").length;
  const facts = [`${done}/${steps.length} steps done`, failed > 0 ? `${failed} failed` : "no failed steps"];
  return { id: "system", label: "System", status: failed > 0 ? "a step failed" : "steps healthy", tone: failed > 0 ? "fail" : "recorded", facts };
};

/** The causal rail: released episodes as passed steps, the held-out event as the outlined next step. */
export function trajectory(view: EpisodeReplayView): TrajectoryStep[] {
  const steps: TrajectoryStep[] = view.prior_episodes.map((e) => ({
    position: e.position,
    released: e.released,
    material: e.material,
    heldOut: e.held_out,
    label: e.source_system,
  }));
  if (view.next_event) {
    steps.push({
      position: view.next_event.position,
      released: false,
      material: null,
      heldOut: true,
      label: view.next_event.source_system,
    });
  }
  return steps;
}

/** Compact change lines for the "recent material changes" list, newest last. */
export function materialChanges(view: EpisodeReplayView): MaterialChange[] {
  return view.prior_episodes
    .filter((e) => e.material)
    .map((e) => ({
      position: e.position,
      label: `${e.source_system} ${MATERIAL_LABEL[String(true)]}`,
      detail: [e.state_version != null ? `state v${e.state_version}` : null, e.graph_diff_id != null ? `graph diff ${e.graph_diff_id}` : null]
        .filter(Boolean)
        .join(" · ") || null,
      decisionEpisodeId: e.decision_episode_id,
    }));
}

export function buildControlView(
  view: EpisodeReplayView,
  accountName: string | null,
  run: AgentRun | null,
  strategies: RunStrategies | null,
  decision: HumanStrategyDecision | null,
  cliffKinds: readonly string[] | null,
  backendUnavailable = false,
): ControlView {
  const latestDecision = [...view.prior_episodes].reverse().find((e) => e.decision_episode_id)?.decision_episode_id ?? null;
  return {
    accountId: view.account_id,
    opportunityId: view.opportunity_id,
    manifestId: view.manifest_id,
    accountName,
    episode: view.episode,
    total: view.total,
    window: view.window,
    computedAt: view.computed_at,
    bands: [intelligenceBand(view), decisionBand(run, strategies, decision, backendUnavailable), cliffBand(cliffKinds, latestDecision), systemBand(run, backendUnavailable)],
    changes: materialChanges(view),
    trajectory: trajectory(view),
    canPlayNext: view.can_play_next,
    canPrevious: view.can_previous,
    nextEvent: view.next_event
      ? { position: view.next_event.position, occurredAt: view.next_event.occurred_at, source: view.next_event.source_system, provenance: view.next_event.provenance }
      : null,
    stateVersion: view.state?.version ?? null,
    stateDigest: view.state?.digest ?? null,
    stateAsOf: view.state?.as_of ?? null,
    knowledgeCount: view.knowledge.items.length,
    run,
    decisionEpisodeId: latestDecision,
  };
}
