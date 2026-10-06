import type { Activity } from "@/lib/api/types";

/** A valid ISO instant normalised to UTC, or null (absent or unparseable cutoffs are ignored). */
export function parseCutoff(raw: string | undefined): string | null {
  if (!raw) return null;
  const ms = Date.parse(raw);
  return Number.isNaN(ms) ? null : new Date(ms).toISOString();
}

/**
 * The same instant plus one microsecond (the resolution of `timestamptz`), keeping the exact
 * fractional seconds of the input. `world_as_of` is strict-before, so the After-Play world read uses
 * `occurred_at(N) + 1µs` to include event N itself (ADR-0019 §3). Returns the input unchanged when it
 * is not a UTC RFC 3339 instant.
 */
export function addMicrosecond(iso: string): string {
  const m = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(iso);
  if (!m) return iso;
  const base = m[1]!;
  const micros = (m[2] ?? "").padEnd(6, "0").slice(0, 6);
  const next = BigInt(micros) + 1n;
  if (next < 1_000_000n) return `${base}.${next.toString().padStart(6, "0")}Z`;
  const d = new Date(`${base}Z`);
  d.setUTCSeconds(d.getUTCSeconds() + 1);
  return `${d.toISOString().slice(0, 19)}.000000Z`;
}

/**
 * Activities in time order (oldest first). With a cutoff, only activities strictly before it, the same
 * semantics as the core's `before` keyset cursor: the held-out event N sits at the cutoff, history is N-1.
 */
export function timelineUpTo(activities: readonly Activity[], cutoff: string | null): Activity[] {
  const limit = cutoff === null ? Infinity : Date.parse(cutoff);
  return activities
    .filter((a) => Date.parse(a.occurred_at) < limit)
    .sort((a, b) => Date.parse(a.occurred_at) - Date.parse(b.occurred_at) || a.id.localeCompare(b.id));
}
