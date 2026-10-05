// @vitest-environment jsdom
// The /control replay card's secondary link to the account's Before Play history: a plain link, never a trigger.
import { cleanup, render, screen } from "@testing-library/react";
import { isValidElement, type ReactElement, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { accountViewHref } from "@/lib/view/account-href";
import { historyLinkLabel } from "@/lib/view/history-link";
import { HistoryLink } from "@/components/control/HistoryLink";
import { PipelineStrip } from "@/components/control/PipelineStrip";

vi.mock("@/lib/cached-loaders", () => ({
  getControlPage: vi.fn(async () => ({
    replay: { notices: [], view: {} },
    control: { accountId: "acc-1", accountName: "A", episode: 30, total: 31, window: "w", stateVersion: 1, run: null, opportunityId: null, canPlayNext: true, nextEvent: { position: 31, occurredAt: "t", source: "s", provenance: null }, progress: null, bands: [], changes: [], manifestId: "m", trajectory: [] },
  })),
}));
vi.mock("@/app/control/actions", () => ({ playEvent: vi.fn() }));
const { default: ControlPage } = await import("@/app/control/page");

afterEach(cleanup);

const ID = "0a0c0000-0000-4000-8000-000000000001";

describe("accountViewHref", () => {
  it("builds the before view, optionally with the event and cutoff", () => {
    expect(accountViewHref(ID, {}, "before")).toBe(`/accounts/${ID}?view=before`);
    expect(accountViewHref(ID, { event: "e", cutoff: "c" }, "after")).toBe(`/accounts/${ID}?view=after&event=e&cutoff=c`);
  });
});

describe("historyLinkLabel", () => {
  it("names the count of events before Event N", () => {
    expect(historyLinkLabel(13)).toBe("View history (12 events before Event 13)");
    expect(historyLinkLabel(2)).toBe("View history (1 event before Event 2)");
  });
  it.each([[1], [0], [-1], [Number.NaN], [null], [undefined], [2.5]])("falls back when the next position is %s", (n) => {
    expect(historyLinkLabel(n as number | null | undefined)).toBe("View history before the next event");
  });
});

describe("HistoryLink", () => {
  it("renders a plain link to the Before Play view with the count", () => {
    render(<HistoryLink accountId={ID} nextPosition={31} demo={false} />);
    const a = screen.getByRole("link", { name: "View history (30 events before Event 31)" });
    expect(a.getAttribute("href")).toBe(`/accounts/${ID}?view=before`);
    expect(a.closest("form")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
  });
  it("preserves demo=1", () => {
    render(<HistoryLink accountId={ID} nextPosition={31} demo />);
    expect(screen.getByRole("link").getAttribute("href")).toBe(`/accounts/${ID}?view=before&demo=1`);
  });
  it("uses the fallback label without a count", () => {
    render(<HistoryLink accountId={ID} nextPosition={null} demo={false} />);
    expect(screen.getByRole("link", { name: "View history before the next event" })).toBeTruthy();
  });
});

function find(node: ReactNode, type: unknown): ReactElement | null {
  if (!isValidElement(node)) return Array.isArray(node) ? node.map((n) => find(n, type)).find(Boolean) ?? null : null;
  if (node.type === type) return node;
  return find((node.props as { children?: ReactNode }).children, type);
}

describe("/control replay card", () => {
  const page = (sp: Record<string, string>) => ControlPage({ searchParams: Promise.resolve({ manifest: "0d3a0000-0000-4000-8000-000000000501", ...sp }) });
  it("passes the account, the released count and the demo flag to the link", async () => {
    const link = find(await page({ demo: "1" }), HistoryLink);
    expect(link?.props).toMatchObject({ accountId: "acc-1", nextPosition: 31, demo: true });
  });
  it("leaves demo off without ?demo=1", async () => {
    expect(find(await page({}), HistoryLink)?.props).toMatchObject({ demo: false });
  });
  it("passes no position when nothing is held out, so the count is never the released index", async () => {
    const { getControlPage } = await import("@/lib/cached-loaders");
    const base = await (getControlPage as unknown as () => Promise<{ replay: unknown; control: Record<string, unknown> }>)();
    vi.mocked(getControlPage).mockResolvedValueOnce({ ...base, control: { ...base.control, nextEvent: null } } as never);
    expect(find(await page({}), HistoryLink)?.props).toMatchObject({ nextPosition: null });
  });
  it("keeps the Play strip as the only trigger beside the link", async () => {
    expect(find(await page({}), PipelineStrip)).not.toBeNull();
  });
});
