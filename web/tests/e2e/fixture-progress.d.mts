// Types for fixture-progress.mjs (what the fixture core answers for the Play progress read).
/* eslint-disable @typescript-eslint/no-explicit-any */
export const STAGES: readonly string[];

export function progressDocument(input: {
  play: { startedAt: number | null; event: any };
  world: { manifest_id: string; account_id: string };
  playMs: number;
  now?: number;
}): any;
