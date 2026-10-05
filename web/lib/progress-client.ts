// Browser side of the Play strip's poll: one same-origin GET to the web app's own progress route, which reads the core
// with the server-held token. The result keeps "the backend could not be reached" apart from "the manifest is unknown",
// so a transport problem is never rendered as a pipeline failure.
import type { PipelineProgress } from "@/lib/api/types";

export type ProgressRead = { ok: true; progress: PipelineProgress } | { ok: false; reason: "unavailable" | "not_found" };

export const PROGRESS_ROUTE = "/api/replay-progress";

export async function fetchReplayProgress(manifestId: string, fetchImpl: typeof fetch = fetch): Promise<ProgressRead> {
  try {
    const res = await fetchImpl(`${PROGRESS_ROUTE}/${encodeURIComponent(manifestId)}`, { cache: "no-store", headers: { Accept: "application/json" } });
    if (res.status === 404) return { ok: false, reason: "not_found" };
    if (!res.ok) return { ok: false, reason: "unavailable" };
    const body: unknown = await res.json();
    if (typeof body !== "object" || body === null || !Array.isArray((body as PipelineProgress).stages)) return { ok: false, reason: "unavailable" };
    return { ok: true, progress: body as PipelineProgress };
  } catch {
    return { ok: false, reason: "unavailable" };
  }
}
