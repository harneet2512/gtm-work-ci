// "Pick an episode" (HAR-149 section 2): the episodes in the database, newest first, in the words of the event that started each.
import Link from "next/link";
import { withDemo } from "@/lib/view/demo-link";
import type { EpisodeSummary } from "@/lib/api/types";
import { formatDay } from "@/lib/format";
import { isTestData, TEST_DATA_TITLE } from "@/lib/evals/inspector/test-data";

export interface PickerEpisode {
  episodeId: string;
  label: string;
  summary: EpisodeSummary;
  /** Stored results of the episode (all buckets). */
  resultCount: number;
}

export function EpisodePicker({ episodes, demo }: { episodes: readonly PickerEpisode[]; demo: boolean }) {
  if (episodes.length === 0) return <p className="hint">No episode has been evaluated yet.</p>;
  return (
    <ul className="picker">
      {episodes.map((e) => (
        <li key={e.episodeId}>
          <Link href={withDemo(`/evals/episode/${e.episodeId}`, demo)} className="picker-card">
            <span className="picker-account">{isTestData() ? TEST_DATA_TITLE : e.summary.account_name}</span>
            <span className="picker-event">{e.summary.triggering_event?.summary ?? "An event with no recorded summary"}</span>
            <span className="picker-meta">
              {e.summary.triggering_event ? `${formatDay(e.summary.triggering_event.occurred_at)} · ` : ""}
              {`${e.resultCount} stored ${e.resultCount === 1 ? "result" : "results"}`}
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
