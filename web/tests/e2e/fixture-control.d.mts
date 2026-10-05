// Types for fixture-control.mjs and fixture-progress.mjs (the fixture core's control-plane and Play progress reads).
/* eslint-disable @typescript-eslint/no-explicit-any */
export interface Reply {
  status: number;
  body?: any;
  error?: { code: string; message: string };
}

export interface FixtureEntry {
  run: any;
  trace: any;
  strategies: any;
  decision: any;
  episodeId: string;
  inference: any;
}

export interface Documents {
  summary: any;
  trace: any;
  mutations: any[];
  metrics: any;
  recomputation: any;
  evalRun: any | null;
  families: any | null;
}

export function controlReply(url: URL): Reply | null;
export function paged(url: URL, items: unknown[]): Reply;
export function documentsOf(entry: FixtureEntry): Documents;
export function allEntries(): FixtureEntry[];
export function compareEntries(a: FixtureEntry, b: FixtureEntry): any;
