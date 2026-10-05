// The per-request loader cache (HAR-145 review MEDIUM 5): a page and its @inspector slot render in the same request,
// so each loader is wrapped in React cache() and runs once per distinct argument list instead of twice.
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  api: { tag: "core-client" },
  loadEpisodePage: vi.fn(async (_api: unknown, id: string, manifest: string | null) => ({ id, manifest })),
  loadExplorer: vi.fn(async (_api: unknown, limit?: number) => ({ limit })),
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
vi.mock("@/lib/load-eval-results", () => ({ loadExplorer: mocks.loadExplorer }));
vi.mock("@/lib/load-control", () => ({ loadControlPage: mocks.loadControlPage }));

const { getControlPage, getEpisodePage, getExplorer } = await import("@/lib/cached-loaders");

beforeEach(() => {
  vi.clearAllMocks();
});

describe("cached loaders", () => {
  it("runs the episode loader once for the page and its inspector slot", async () => {
    const [a, b] = await Promise.all([getEpisodePage("e1", "m1"), getEpisodePage("e1", "m1")]);
    expect(a).toBe(b);
    expect(mocks.loadEpisodePage).toHaveBeenCalledTimes(1);
    expect(mocks.loadEpisodePage).toHaveBeenCalledWith(mocks.api, "e1", "m1");
  });

  it("loads again for a different episode or manifest", async () => {
    await getEpisodePage("e1", null);
    await getEpisodePage("e2", null);
    expect(mocks.loadEpisodePage).toHaveBeenCalledTimes(2);
  });

  it("runs the explorer and control loaders once per argument list", async () => {
    await Promise.all([getExplorer(), getExplorer()]);
    await Promise.all([getControlPage("m1", undefined), getControlPage("m1", undefined)]);
    expect(mocks.loadExplorer).toHaveBeenCalledTimes(1);
    expect(mocks.loadControlPage).toHaveBeenCalledTimes(1);
    expect(mocks.loadControlPage).toHaveBeenCalledWith(mocks.api, "m1", undefined);
  });
});
