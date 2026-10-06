// The run page's recomputation read: a 404 is nothing to show, a failure is "backend unavailable".
import { describe, expect, it, vi } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import { loadRecomputation } from "@/lib/load-recomputation";
import type { DependencyInvalidation } from "@/lib/api/types";
import { loadExample } from "./contract-validator";

const doc = loadExample<DependencyInvalidation>("dependency_invalidation");
const RUN = doc.run_id;

describe("loadRecomputation", () => {
  it("returns the served document", async () => {
    const api = { getRunRecomputation: vi.fn(async () => doc) };
    await expect(loadRecomputation(api, RUN)).resolves.toEqual({ recomputation: doc, unavailable: false });
    expect(api.getRunRecomputation).toHaveBeenCalledWith(RUN);
  });

  it("treats a run with no decision episode (null) as nothing to show, not as unavailable", async () => {
    await expect(loadRecomputation({ getRunRecomputation: async () => null }, RUN)).resolves.toEqual({ recomputation: null, unavailable: false });
  });

  it("reads a failed read as backend unavailable", async () => {
    const api = {
      getRunRecomputation: async (): Promise<never> => {
        throw new CoreError(500, "internal", "boom");
      },
    };
    await expect(loadRecomputation(api, RUN)).resolves.toEqual({ recomputation: null, unavailable: true });
  });
});
