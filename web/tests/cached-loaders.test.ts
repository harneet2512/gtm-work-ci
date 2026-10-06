// The per-request loader cache (HAR-145 review MEDIUM 5): a page and its @inspector slot render in the same request,
// so each loader is wrapped in React cache() and runs once per distinct argument list instead of twice.
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  api: { tag: "core-client" },
  loadEpisodePage: vi.fn(async (_api: unknown, id: string) => ({ id })),
  loadEvalRuns: vi.fn(async (_api: unknown, opts: { cursor?: string }) => ({ cursor: opts.cursor })),
  loadFamilies: vi.fn(async (_api: unknown, id: string) => ({ id })),
  loadControlPage: vi.fn(async (_api: unknown, manifest: string, at: string | undefined) => ({ manifest, at })),
}));

// A request-scoped memo, like React's cache() inside one server render.
vi.mock("react", () => ({
  cache: <A extends unknown[], R>(fn: (...a: A) => R) => {
    const memo = new Map<string, R>();
    return (...a: A): R => {
      const key = JSON.stringify(a);
      if (!memo.has(key)) memo.set(key, fn(...a));
      return memo.get(key)!;
    };
  },
}));
vi.mock("@/lib/api/server", () => ({ core: () => mocks.api }));
vi.mock("@/lib/load-episode", () => ({ loadEpisodePage: mocks.loadEpisodePage }));
vi.mock("@/lib/load-eval-runs", () => ({ loadEvalRuns: mocks.loadEvalRuns, loadFamilies: mocks.loadFamilies }));
vi.mock("@/lib/load-control", () => ({ loadControlPage: mocks.loadControlPage }));

const { getControlPage, getEpisodePage, getEvalFamilies, getEvalRuns } = await import("@/lib/cached-loaders");

beforeEach(() => {
  vi.clearAllMocks();
});

describe("cached loaders", () => {
  it("runs the episode loader once for the page and its inspector slot", async () => {
    const [a, b] = await Promise.all([getEpisodePage("e1"), getEpisodePage("e1")]);
    expect(a).toBe(b);
    expect(mocks.loadEpisodePage).toHaveBeenCalledTimes(1);
    expect(mocks.loadEpisodePage).toHaveBeenCalledWith(mocks.api, "e1");
  });

  it("loads again for a different episode", async () => {
    await getEpisodePage("e3");
    await getEpisodePage("e4");
    expect(mocks.loadEpisodePage).toHaveBeenCalledTimes(2);
  });

  it("runs the eval-run, family and control loaders once per argument list", async () => {
    await Promise.all([getEvalRuns(undefined), getEvalRuns(undefined)]);
    await Promise.all([getEvalFamilies("r1"), getEvalFamilies("r1")]);
    await Promise.all([getControlPage("m1", undefined), getControlPage("m1", undefined)]);
    expect(mocks.loadEvalRuns).toHaveBeenCalledTimes(1);
    expect(mocks.loadEvalRuns).toHaveBeenCalledWith(mocks.api, { cursor: undefined });
    expect(mocks.loadFamilies).toHaveBeenCalledTimes(1);
    expect(mocks.loadControlPage).toHaveBeenCalledTimes(1);
    expect(mocks.loadControlPage).toHaveBeenCalledWith(mocks.api, "m1", undefined);
  });
});
