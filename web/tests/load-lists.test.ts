// The home and runs loaders (HAR-145): keyset pages; a refused cursor or filter and an outage keep their own meaning.
import { describe, expect, it, vi } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import { LIST_PAGE_SIZE, loadAccounts, loadRuns } from "@/lib/load-lists";
import type { AccountSummary, AgentRun } from "@/lib/api/types";

const account = { id: "0a0c0000-0000-4000-8000-000000000001", name: "Acme" } as AccountSummary;
const run = { id: "0f0a0000-0000-4000-8000-000000000601" } as AgentRun;
const refuse = (status: number) => new CoreError(status, "x", "x");

describe("loadAccounts", () => {
  it("reads one page with its next cursor", async () => {
    const api = { listAccountsPage: vi.fn(async () => ({ items: [account], nextCursor: "n2" })) };
    await expect(loadAccounts(api, "c1")).resolves.toEqual({ items: [account], nextCursor: "n2", unavailable: false, badCursor: false, badFilter: false });
    expect(api.listAccountsPage).toHaveBeenCalledWith({ limit: LIST_PAGE_SIZE, cursor: "c1" });
  });

  it("falls back to the first page, and says so, when the cursor is refused", async () => {
    const list = vi.fn(async (o?: { cursor?: string }) => {
      if (o?.cursor) throw refuse(400);
      return { items: [account], nextCursor: null };
    });
    await expect(loadAccounts({ listAccountsPage: list }, "junk")).resolves.toMatchObject({ items: [account], badCursor: true, unavailable: false });
  });

  it("reads an outage as backend unavailable, never as no accounts", async () => {
    const api = { listAccountsPage: vi.fn(async (): Promise<never> => { throw refuse(500); }) };
    await expect(loadAccounts(api)).resolves.toMatchObject({ items: [], unavailable: true });
    const down = { listAccountsPage: vi.fn(async (): Promise<never> => { throw new CoreError(0, "unreachable", "x"); }) };
    await expect(loadAccounts(down, "c")).resolves.toMatchObject({ unavailable: true, badCursor: false });
  });
});

describe("loadRuns", () => {
  it("passes the filters and the cursor through", async () => {
    const api = { listRunsPage: vi.fn(async () => ({ items: [run], nextCursor: null })) };
    await loadRuns(api, { accountId: account.id, status: "executed", cursor: "c9" });
    expect(api.listRunsPage).toHaveBeenCalledWith({ limit: LIST_PAGE_SIZE, accountId: account.id, status: "executed", cursor: "c9" });
  });

  it("falls back to the first page of the same filter when the cursor is refused", async () => {
    const list = vi.fn(async (o?: { cursor?: string }) => {
      if (o?.cursor) throw refuse(400);
      return { items: [run], nextCursor: null };
    });
    const data = await loadRuns({ listRunsPage: list }, { status: "executed", cursor: "junk" });
    expect(data).toMatchObject({ items: [run], badCursor: true, badFilter: false });
    expect(list).toHaveBeenLastCalledWith({ limit: LIST_PAGE_SIZE, status: "executed", cursor: undefined });
  });

  it("shows nothing, and says the filter was refused, for a status the core does not know", async () => {
    const api = { listRunsPage: vi.fn(async (): Promise<never> => { throw refuse(400); }) };
    await expect(loadRuns(api, { status: "bogus" })).resolves.toMatchObject({ items: [], badFilter: true, unavailable: false });
  });

  it("reads an outage as backend unavailable, never as no runs", async () => {
    const api = { listRunsPage: vi.fn(async (): Promise<never> => { throw refuse(503); }) };
    await expect(loadRuns(api)).resolves.toMatchObject({ items: [], unavailable: true, badFilter: false });
  });
});
