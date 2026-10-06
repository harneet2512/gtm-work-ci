// @vitest-environment jsdom
// The explorer with its engine running on real animation frames (d3-timer binds the clock and the frame
// scheduler when it loads, so fake timers cannot drive its transitions): canvas clicks reach the workspace's
// selection, and the theme, reduced motion, the canvas size and the web font all reach the engine.
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AccountMap } from "@/components/AccountMap";
import { indexClaims } from "@/lib/graph/claim-index";
import { buildExplorerModel } from "@/lib/graph/model";
import { indexDiff } from "@/lib/view/diff";
import type { AccountState, Activity, Graph } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";
import { fakeContext } from "./graph-canvas-fake";

const graph = loadFixture<Graph>("acme.graph.json");
const state = loadExample<AccountState>("account_state");
const activities = loadFixture<{ items: Activity[] }>("acme.timeline.json").items;
const ACCOUNT = "0a0c0000-0000-4000-8000-000000000001";

let motionListener: (() => void) | null = null;
let resizeCallback: (() => void) | null = null;
let reduce = false;

beforeEach(() => {
  const fake = fakeContext();
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation((() => fake.ctx) as never);
  reduce = false;
  window.matchMedia = ((q: string) => ({
    get matches() {
      return reduce;
    },
    media: q,
    addEventListener: (_: string, cb: () => void) => void (motionListener = cb),
    removeEventListener: () => void (motionListener = null),
  })) as unknown as typeof window.matchMedia;
  globalThis.ResizeObserver = class {
    constructor(cb: () => void) {
      resizeCallback = cb;
    }
    observe() {}
    disconnect() {
      resizeCallback = null;
    }
  } as unknown as typeof ResizeObserver;
  Object.defineProperty(document, "fonts", { configurable: true, value: { ready: Promise.resolve() } });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

let mounts = 0;

/** Each test opens its own account, so the layout memory (shared by the page, by design) does not leak between tests. */
function mount() {
  const onSelect = vi.fn();
  const onClear = vi.fn();
  mounts += 1;
  const own = { ...graph, account_id: `acc-live-${mounts}` };
  const model = buildExplorerModel({ graph: own, marks: indexDiff(null), showMarks: false, activities, state, eventId: null });
  render(<AccountMap model={model} marks={indexDiff(null)} playView={null} claims={indexClaims(state)} history={new Map()} details={null} coverage="" selectedId={null} onSelect={onSelect} onClear={onClear} />);
  return { onSelect, onClear };
}

// Real animation frames slow down on a loaded runner (the full suite runs three workers with coverage).
vi.setConfig({ testTimeout: 60_000 });
const WAIT = { timeout: 45_000, interval: 50 };
const stageOf = (): HTMLElement => canvas().parentElement!;

async function settle(): Promise<void> {
  await waitFor(() => expect(stageOf().dataset.settled).toBe("true"), WAIT);
}

const twin = (id: string): HTMLElement => document.querySelector<HTMLElement>(`[data-node-id="${id}"]`)!;
const canvas = (): HTMLCanvasElement => screen.getByRole("application", { name: "Account graph" }) as HTMLCanvasElement;
const at = (id: string) => ({ clientX: Number(twin(id).dataset.sx), clientY: Number(twin(id).dataset.sy), bubbles: true });

describe("AccountMap on a live engine", () => {
  it("settles, then routes canvas clicks, double-clicks and background clicks to the workspace", async () => {
    const { onSelect, onClear } = mount();
    await settle();
    const stage = canvas().parentElement!;
    expect(stage.dataset.settled).toBe("true");
    canvas().dispatchEvent(new MouseEvent("click", at(ACCOUNT)));
    expect(onSelect.mock.calls.at(-1)![0]).toMatchObject({ id: ACCOUNT, title: "Acme Corp" });
    canvas().dispatchEvent(new MouseEvent("dblclick", at(ACCOUNT)));
    await waitFor(() => expect(Number(stage.dataset.zoom)).toBeGreaterThanOrEqual(3), WAIT);
    canvas().dispatchEvent(new MouseEvent("click", { clientX: -500, clientY: -500, bubbles: true }));
    expect(onClear).toHaveBeenCalledOnce();
  });

  it("animates camera moves when motion is allowed, and lands them at once when it is reduced", async () => {
    mount();
    const stage = canvas().parentElement!;
    const fitted = Number(stage.dataset.zoom);
    screen.getByRole("button", { name: "Zoom in" }).click();
    expect(Number(stage.dataset.zoom)).toBeCloseTo(fitted, 1);
    // A resize report (or the web font arriving) mid-flight must not cut the user's zoom short.
    act(() => resizeCallback?.());
    await waitFor(() => expect(Number(stage.dataset.zoom)).toBeCloseTo(fitted * 1.4, 1), WAIT);
    // Rapid clicks compose from where the camera is going, not from a frame in mid-flight.
    screen.getByRole("button", { name: "Zoom in" }).click();
    screen.getByRole("button", { name: "Zoom in" }).click();
    await waitFor(() => expect(Number(stage.dataset.zoom)).toBeCloseTo(fitted * 1.4 ** 3, 1), WAIT);
    screen.getByRole("button", { name: "Zoom out" }).click();
    screen.getByRole("button", { name: "Zoom out" }).click();
    await waitFor(() => expect(Number(stage.dataset.zoom)).toBeCloseTo(fitted * 1.4, 1), WAIT);
    reduce = true;
    act(() => motionListener?.());
    screen.getByRole("button", { name: "Fit the whole graph" }).click();
    expect(Number(stage.dataset.zoom)).toBeCloseTo(fitted, 1);
  });

  it("follows the theme, the canvas size and the web font without rebuilding", async () => {
    mount();
    await settle();
    const stage = canvas().parentElement!;
    const k = stage.dataset.zoom;
    await act(async () => {
      document.documentElement.setAttribute("data-theme", "dark");
      await Promise.resolve();
    });
    act(() => resizeCallback?.());
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(stage.dataset.zoom).toBe(k);
    expect(stage.dataset.settled).toBe("true");
    document.documentElement.removeAttribute("data-theme");
  });
});
