// The /control operator view (HAR-145): four product-area health bands (lib/view/bands.ts, from the account's latest
// EvalRun), the recent material changes and the episode trajectory: pure derivations over the replay view and what
// the core served. A band that has no data says so; nothing is fabricated.
import type { AgentRun, EpisodeReplayView, EpisodeSummary, EvalRun, PipelineProgress } from "@/lib/api/types";
import { buildBands, type HealthBand } from "@/lib/view/bands";

export type { AreaId, HealthBand } from "@/lib/view/bands";

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
  /** The latest episode that opened a decision. */
  decisionEpisodeId: string | null;
  /** The account's latest EvalRun, which the bands summarise. */
  evalRunId: string | null;
  /** The pipeline's progress at load time (the strip's starting point). */
  progress: PipelineProgress | null;
}

const MATERIAL_LABEL: Record<string, string> = {
  true: "changed the world",
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

export interface ControlInputs {
  view: EpisodeReplayView;
  accountName: string | null;
  /** The account's newest run: the inspector's source. */
  run: AgentRun | null;
  evalRun: EvalRun | null;
  /** The latest decision episode's summary, when it could be read. */
  episode: EpisodeSummary | null;
  /** Posted Cliff message kinds; null when the surface refs could not be read. */
  cliffKinds: readonly string[] | null;
  /** A read failed for a reason other than "none exist": the bands say "backend unavailable". */
  backendUnavailable: boolean;
  /** What the pipeline already did for this manifest, so a reload shows it; null when it could not be read. */
  progress: PipelineProgress | null;
}

const intelligenceFacts = (view: EpisodeReplayView): string[] => {
  const material = view.prior_episodes.filter((e) => e.material).length;
  const facts = [`${material} material event${material === 1 ? "" : "s"} released of ${view.episode}`];
  if (view.state) facts.push(`account state v${view.state.version} as of ${view.state.as_of ?? "unknown"}`);
  if (view.knowledge.items.length > 0) facts.push(`${view.knowledge.items.length} knowledge item${view.knowledge.items.length === 1 ? "" : "s"} in scope`);
  return facts;
};

export function buildControlView(i: ControlInputs): ControlView {
  const { view } = i;
  const latestDecision = [...view.prior_episodes].reverse().find((e) => e.decision_episode_id)?.decision_episode_id ?? null;
  return {
    accountId: view.account_id,
    opportunityId: view.opportunity_id,
    manifestId: view.manifest_id,
    accountName: i.accountName,
    episode: view.episode,
    total: view.total,
    window: view.window,
    computedAt: view.computed_at,
    bands: buildBands(i.evalRun, { cliffKinds: i.cliffKinds, episode: i.episode, intelligenceFacts: intelligenceFacts(view), unavailable: i.backendUnavailable }),
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
    run: i.run,
    decisionEpisodeId: latestDecision,
    evalRunId: i.evalRun?.id ?? null,
    progress: i.progress,
  };
}
