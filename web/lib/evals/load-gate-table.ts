// Loads the evals table: the newest episodes that have an eval run (or the one named by ?episode=), each with its stored gate
// results, its trace (for span times and evidence links) and its account's activities (for the real quotes). "Backend
// unavailable" (a read threw) is a different status from "nothing measured yet" (an empty list): neither is a FAIL.
import type { CoreClient } from "@/lib/api/core-client";
import { formatDay } from "@/lib/format";
import { UUID } from "@/lib/uuid";
import { makeResolver, type ActivityLite } from "./gate-evidence";
import { buildGateRows, type EpisodeInput, type GateCatalogEntry, type GateRow } from "./gate-table";

const MAX_EPISODES = 6;

export type GateTableApi = Pick<CoreClient, "listEvalRunsPage" | "getEpisode" | "getEpisodeTrace" | "listEpisodeGateResults" | "getTimeline">;

export interface SpanStepLite {
  id: string;
  seq: number;
  title: string;
  time: string | null;
}

export interface GateTableData {
  status: "ok" | "unavailable";
  rows: GateRow[];
  /** The episode's spans in causal order, by episode id (the Trace tab's path). */
  paths: Record<string, SpanStepLite[]>;
  /** Episodes that could not be read (a notice, not a verdict). */
  unreadable: string[];
}

async function episodeInput(api: GateTableApi, id: string): Promise<{ input: EpisodeInput; path: SpanStepLite[] } | null> {
  const summary = await api.getEpisode(id);
  if (!summary) return null;
  const [trace, results, timeline] = await Promise.all([
    api.getEpisodeTrace(id),
    api.listEpisodeGateResults(id),
    api.getTimeline(summary.account_id).catch(() => []),
  ]);
  const spans = (trace?.spans ?? []).map((s) => ({ id: s.id, kind: s.kind, title: s.title, refs: s.refs }));
  const activities = new Map<string, ActivityLite>(timeline.map((a) => [a.id, { id: a.id, summary: a.summary ?? null, activityType: a.activity_type, occurredAt: a.occurred_at }]));
  const path = [...(trace?.spans ?? [])].sort((a, b) => a.seq - b.seq).map((s) => ({ id: s.id, seq: s.seq, title: s.title, time: s.occurred_at }));
  const input: EpisodeInput = {
    episode: { id, label: summary.triggering_event ? `${summary.account_name} · ${formatDay(summary.triggering_event.occurred_at)}` : summary.account_name, accountId: summary.account_id },
    results: results ?? [],
    spanTimes: new Map((trace?.spans ?? []).map((s) => [s.id, s.occurred_at])),
    resolve: makeResolver({ episodeId: id, accountId: summary.account_id, spans, activities }),
  };
  return { input, path };
}

/** Two episodes with the same label (one account, one event day) are told apart by a number, never by an id. */
function disambiguate(inputs: EpisodeInput[]): EpisodeInput[] {
  const seen = new Map<string, number>();
  return inputs.map((i) => {
    const n = seen.get(i.episode.label) ?? 0;
    seen.set(i.episode.label, n + 1);
    return inputs.filter((o) => o.episode.label === i.episode.label).length > 1 ? { ...i, episode: { ...i.episode, label: `${i.episode.label} (${n + 1})` } } : i;
  });
}
export async function loadGateTable(api: GateTableApi, catalog: readonly GateCatalogEntry[], requested?: string): Promise<GateTableData> {
  try {
    const ids: string[] = [];
    const want = requested?.trim().toLowerCase();
    if (want && UUID.test(want)) ids.push(want);
    const page = await api.listEvalRunsPage({ limit: 20 });
    for (const r of page.items) if (r.decision_episode_id && !ids.includes(r.decision_episode_id)) ids.push(r.decision_episode_id);
    const picked = ids.slice(0, MAX_EPISODES);
    const settled = await Promise.allSettled(picked.map((id) => episodeInput(api, id)));
    const inputs: EpisodeInput[] = [];
    const paths: Record<string, SpanStepLite[]> = {};
    const unreadable: string[] = [];
    settled.forEach((s, i) => {
      if (s.status === "fulfilled" && s.value) {
        inputs.push(s.value.input);
        paths[s.value.input.episode.id] = s.value.path;
      }
      else if (s.status === "rejected") unreadable.push(picked[i]!);
    });
    if (inputs.length === 0 && unreadable.length > 0) return { status: "unavailable", rows: [], paths, unreadable };
    return { status: "ok", rows: buildGateRows(disambiguate(inputs), catalog), paths, unreadable };
  } catch {
    return { status: "unavailable", rows: [], paths: {}, unreadable: [] };
  }
}
