import type { StatusResult } from "./types";

/**
 * Where the browser lands: the control plane on the active case's frozen replay (demo mode on). Null when there is no
 * active seeded case or the demo service is not configured/reachable, so the caller falls back to the ordinary page.
 */
export function landingTarget(result: StatusResult): string | null {
  if (result.kind !== "ok") return null;
  const active = result.status.cases.find((c) => c.active && c.seeded && c.manifest_id);
  return active?.manifest_id ? `/control?manifest=${active.manifest_id}&demo=1` : null;
}

/**
 * Whether the demo continues past this account: the manifest belongs to a case that has a later, frozen case after it.
 * Play then stays available once this account's episodes are released and releases the later account's Event N.
 */
export function handoffAvailable(result: StatusResult, manifestId: string): boolean {
  if (result.kind !== "ok") return false;
  const { cases } = result.status;
  const at = cases.findIndex((c) => c.seeded && c.manifest_id === manifestId);
  const later = at < 0 ? undefined : cases[at + 1];
  return later !== undefined && later.seeded && !!later.manifest_id;
}
