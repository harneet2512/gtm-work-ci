// The run page's edit-recomputation read (HAR-145). A run with no decision episode answers 404: that is "nothing to show"
// (null). A transport or server failure is "backend unavailable", which the page says instead of hiding the section.
import type { CoreClient } from "@/lib/api/core-client";
import type { DependencyInvalidation } from "@/lib/api/types";

export interface RecomputationRead {
  recomputation: DependencyInvalidation | null;
  unavailable: boolean;
}

export async function loadRecomputation(api: Pick<CoreClient, "getRunRecomputation">, runId: string): Promise<RecomputationRead> {
  try {
    return { recomputation: await api.getRunRecomputation(runId), unavailable: false };
  } catch {
    return { recomputation: null, unavailable: true };
  }
}
