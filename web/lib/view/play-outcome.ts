// What the strip says when Play does not release the event. A transport failure (the request never reached the core, or
// it timed out) is neutral: nothing is claimed about the pipeline, and the progress read shows what actually ran.
export interface PlayLine {
  text: string;
  /** True only when the core refused Play; a transport problem is not an error of the pipeline. */
  error: boolean;
}

const REFUSALS: Record<string, string> = {
  already_released: "Play has already completed for this event. Nothing changes.",
  play_in_progress: "Another Play of this event is already running. Its progress is shown below.",
  held_out_event_visible: "Play was refused: something derived from the held-out event is already visible in the world. Nothing was released.",
  wrong_event: "Play was refused: that is not the held-out event of this account.",
  release_mismatch: "Play was refused: the released event does not match the recorded event.",
  manifest_not_found: "Play was refused: the demo account was not found.",
  graph_unavailable: "Play was refused: the graph is not available, so nothing was released.",
  replay_source_unavailable: "Play was refused: the replay data is not available, so nothing was released.",
};

export function playRefusalLine(code: string): PlayLine {
  if (code === "unreachable") return { text: "Backend unavailable: Play was not delivered, so nothing was released.", error: false };
  if (code === "timeout") return { text: "Backend unavailable: Play timed out. The progress below shows what ran; press Play again to resume.", error: false };
  return { text: REFUSALS[code] ?? `Play was refused (${code}).`, error: true };
}
