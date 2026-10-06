// Loaders for the home page (accounts) and /runs (HAR-145): keyset pages from the core. A refused cursor or filter (400)
// and an unreachable backend each keep their own meaning: neither is rendered as "no accounts" or "no runs".
import type { CoreClient } from "@/lib/api/core-client";
import { CoreError } from "@/lib/api/core-client";
import type { AccountSummary, AgentRun, Page } from "@/lib/api/types";

export const LIST_PAGE_SIZE = 50;

export interface ListData<T> {
  items: T[];
  /** The cursor of the next page, or null on the last page. */
  nextCursor: string | null;
  /** The read failed (transport or server error): the page says "backend unavailable". */
  unavailable: boolean;
  /** The page marker in the URL was refused (400), so the first page is shown instead. */
  badCursor: boolean;
  /** A filter was refused (400): nothing is shown for it, and the page says why. */
  badFilter: boolean;
}

const empty = <T,>(over: Partial<ListData<T>> = {}): ListData<T> => ({ items: [], nextCursor: null, unavailable: false, badCursor: false, badFilter: false, ...over });
const ok = <T,>(page: Page<T>): ListData<T> => empty({ items: page.items, nextCursor: page.nextCursor });

export async function loadAccounts(api: Pick<CoreClient, "listAccountsPage">, cursor?: string): Promise<ListData<AccountSummary>> {
  try {
    return ok(await api.listAccountsPage({ limit: LIST_PAGE_SIZE, cursor }));
  } catch (e) {
    if (cursor && e instanceof CoreError && e.status === 400) return { ...(await loadAccounts(api)), badCursor: true };
    return empty({ unavailable: true });
  }
}

export interface RunFilter {
  cursor?: string;
  accountId?: string;
  status?: string;
}

export async function loadRuns(api: Pick<CoreClient, "listRunsPage">, filter: RunFilter = {}): Promise<ListData<AgentRun>> {
  try {
    return ok(await api.listRunsPage({ limit: LIST_PAGE_SIZE, ...filter }));
  } catch (e) {
    if (e instanceof CoreError && e.status === 400) {
      // A refused cursor falls back to the first page of the same filter; a refused filter shows nothing and says so.
      if (filter.cursor) return { ...(await loadRuns(api, { ...filter, cursor: undefined })), badCursor: true };
      return empty({ badFilter: true });
    }
    return empty({ unavailable: true });
  }
}
