// Everything the live episode inspector shows for one episode (HAR-149 sections 2, 3 and 4): the path, one drawer per gate and
// the three-candidate screen, built from the episode summary, its trace, its stored gate results, the strategy set, the stored
// ranking, the human decision and inference, and what the edit recomputed. Only the summary is required; every other read
// degrades to a notice and the sections that depend on it say what is missing.
import type { CoreClient } from "@/lib/api/core-client";
import type { EpisodeSummary, GateResult, Knowledge } from "@/lib/api/types";
import { type ActivityLite, makeResolver, type SpanLite } from "@/lib/evals/gate-evidence";
import { buildDecisionScreen, type DecisionScreen } from "./candidates";
import { buildDrawerModel, type DrawerModel } from "./drawer-model";
import { buildEpisodePath, type EpisodePath, PATH_ORDER } from "./episode-path";
import type { FleetEpisode } from "./fleet";
import type { InputItem } from "./inputs";
import { episodeLabel } from "./load-fleet";
import type { InspectorRegistry } from "./load-registry";

type Api = Pick<
  CoreClient,
  | "getEpisode"
  | "getEpisodeTrace"
  | "listEpisodeGateResults"
  | "getTimeline"
  | "getRunStrategies"
  | "getStrategyDecision"
  | "getJudgmentInference"
  | "getRunRecomputation"
  | "getEpisodeRanking"
  | "getKnowledge"
>;

const MAX_KNOWLEDGE = 12;

export interface EpisodeInspector {
  episodeId: string;
  label: string;
  accountName: string;
  /** The real wording of the event that triggered the episode; null when it could not be read. */
  eventSummary: string | null;
  /** Where the human side got to, in words. */
  statusLine: string;
  path: EpisodePath;
  drawers: Record<string, DrawerModel>;
  screen: DecisionScreen;
  notices: string[];
}

const STATUS_WORDS: Readonly<Record<EpisodeSummary["final_status"], string>> = {
  awaiting_choice: "Waiting for the human to choose an option",
  awaiting_send: "Chosen; waiting for the send decision",
  send_recorded: "The human decided to send (recorded; nothing left the building in this dry run)",
  sent: "Sent",
  discarded: "The human discarded the action",
  decided: "Decided",
};

async function attempt<T>(notices: string[], what: string, read: () => Promise<T | null | undefined>): Promise<T | null> {
  try {
    return (await read()) ?? null;
  } catch {
    notices.push(`${what} could not be read right now.`);
    return null;
  }
}

async function readKnowledge(api: Api, ids: readonly string[]): Promise<Map<string, Knowledge>> {
  const out = new Map<string, Knowledge>();
  await Promise.all(
    [...new Set(ids)].slice(0, MAX_KNOWLEDGE).map(async (id) => {
      try {
        out.set(id, await api.getKnowledge(id));
      } catch {
        /* the title stays unknown: the input shows its id-free label */
      }
    }),
  );
  return out;
}

export async function loadEpisodeInspector(api: Api, episodeId: string, registry: InspectorRegistry, fleet: readonly FleetEpisode[]): Promise<EpisodeInspector | null> {
  const summary = await api.getEpisode(episodeId);
  if (!summary) return null;
  const notices: string[] = [];
  const runId = summary.agent_run_id;
  const [trace, results, timeline, strategies, decision, inference, recomputation, ranking] = await Promise.all([
    attempt(notices, "The episode trace", () => api.getEpisodeTrace(episodeId)),
    attempt(notices, "The stored gate results", () => api.listEpisodeGateResults(episodeId)),
    attempt(notices, "The account timeline", () => api.getTimeline(summary.account_id)),
    attempt(notices, "The three options", () => api.getRunStrategies(runId)),
    attempt(notices, "The human's decision", () => api.getStrategyDecision(runId)),
    attempt(notices, "The inference of the human's choice", () => api.getJudgmentInference(episodeId)),
    attempt(notices, "What the edit recomputed", () => api.getRunRecomputation(runId)),
    attempt(notices, "The stored ranking", () => api.getEpisodeRanking(episodeId)),
  ]);
  const all: GateResult[] = results ?? [];
  const knowledge = await readKnowledge(api, strategies?.strategy_set.candidates.flatMap((c) => c.knowledge_refs) ?? []);
  const activities = new Map<string, ActivityLite>((timeline ?? []).map((a) => [a.id, { id: a.id, summary: a.summary ?? null, activityType: a.activity_type, occurredAt: a.occurred_at }]));
  const spans: SpanLite[] = (trace?.spans ?? []).map((s) => ({ id: s.id, kind: s.kind, title: s.title, refs: s.refs }));
  const resolve = makeResolver({ episodeId, accountId: summary.account_id, spans, activities });
  const candidateTitle = new Map((strategies?.strategy_set.candidates ?? []).map((c) => [c.candidate_id, c.title]));
  const titleOf = (id: string): string | null => candidateTitle.get(id) ?? knowledge.get(id)?.title ?? null;
  const delta = inference?.inferred_semantic_delta;
  const editText = delta && !delta.unknown ? delta.statement : (decision?.edits ?? []).map((e) => e.kind.replaceAll("_", " ")).join(", ") || null;
  const screen = buildDecisionScreen({ summary, strategies, results: all, ranking, decision, inference, recomputation, titleOf, evidenceText: (id) => activities.get(id)?.summary ?? null });
  const path = buildEpisodePath({ summary, results: all, defs: registry.defs, rules: registry.rules, editText, recomputation });
  const extra = (gate: string): InputItem[] => extraInputs(gate, screen, editText, summary);
  const drawers: Record<string, DrawerModel> = {};
  for (const gate of PATH_ORDER) {
    const def = registry.defs.find((d) => d.id === gate);
    if (!def) continue;
    drawers[gate] = buildDrawerModel({
      episodeId,
      episodeLabel: episodeLabel(summary),
      def,
      results: all.filter((r) => r.gate === gate),
      rules: registry.rules,
      resolve,
      titleOf,
      extraInputs: extra(gate),
      fleet,
      confidence: gate === "D5" ? (delta?.confidence ?? null) : null,
    });
  }
  return {
    episodeId,
    label: episodeLabel(summary),
    accountName: summary.account_name,
    eventSummary: summary.triggering_event?.summary ?? null,
    statusLine: STATUS_WORDS[summary.final_status],
    path,
    drawers,
    screen,
    notices,
  };
}

/** Inputs a gate has beyond its cited evidence: the stored ranking for D3, the human's choice for D4, the edit for D5 and D7. */
function extraInputs(gate: string, screen: DecisionScreen, editText: string | null, summary: EpisodeSummary): InputItem[] {
  const item = (kind: InputItem["kind"], title: string, text: string | null): InputItem => ({ kind, title, text, source: null, href: null });
  if (gate === "D3" && screen.whyWon) {
    const text = screen.whyWon.reasons.map((r) => `Option ${r.higher} above Option ${r.lower}: ${r.text}`).join("\n");
    return [item("ranking", "The stored ranking", text || screen.whyWon.missing)];
  }
  if (gate === "D4" && screen.human) return [item("human", "The human's choice", screen.human.headline)];
  if ((gate === "D5" || gate === "D7") && summary.human_outcome?.edited) return [item("edit", "The human's edit", editText ?? "The human edited the draft before sending.")];
  return [];
}
