// @vitest-environment jsdom
// The knowledge mutations panel (HAR-145): what the episode did to company knowledge, with the operation (DEMOTE
// included), the scope and the knowledge's version at that time; an unreadable read is not an empty list.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { KnowledgeMutations } from "@/components/episode/KnowledgeMutations";
import type { KnowledgeMutation } from "@/lib/api/types";
import { mutationRow, OPERATION_WORD, SCOPE_WORD } from "@/lib/view/knowledge-mutations";
import { loadExample } from "./contract-validator";

afterEach(cleanup);

const example = loadExample<KnowledgeMutation>("knowledge_mutation");
const mutation = (over: Partial<KnowledgeMutation> = {}): KnowledgeMutation => ({ ...example, ...over });

describe("mutationRow", () => {
  it("keeps the operation verbatim, with a plain word, and shows scope and the version at that time", () => {
    const row = mutationRow(mutation({ operation: "DEMOTE", scope: "account_specific", version: 4, occurred_at: "2026-10-05T10:00:00Z", status_before: "supported", status: "provisional" }));
    expect(row.operation).toBe("DEMOTE");
    expect(row.operationWord).toBe(OPERATION_WORD.DEMOTE);
    expect(row.scope).toBe(SCOPE_WORD.account_specific);
    expect(row.version).toBe("v4 as of 2026-10-05T10:00:00Z");
    expect(row.status).toBe("supported → provisional");
  });

  it("says the status was new when there was none before, and unchanged when it did not move", () => {
    expect(mutationRow(mutation({ status_before: null, status: "candidate" })).status).toBe("new → candidate");
    expect(mutationRow(mutation({ status_before: "supported", status: "supported" })).status).toBe("supported (unchanged)");
  });

  it("words the human verdict and the evidence, and falls back to a dash for a missing key or note", () => {
    const row = mutationRow(mutation({ knowledge_key: null, human_verdict: "pending", evidence: { kind: "customer_reaction", ref_id: example.evidence.ref_id, note: null } }));
    expect(row.key).toBe("—");
    expect(row.human).toBe("awaiting the human's confirmation");
    expect(row.evidence).toBe("a customer reaction");
    expect(mutationRow(mutation({ evidence: { kind: "counterexample", ref_id: example.evidence.ref_id, note: "the buyer declined" } })).evidence).toBe("a counterexample: the buyer declined");
  });

  it("covers every operation and scope the contract allows", () => {
    for (const op of ["CREATE", "SUPPORT", "COUNTEREXAMPLE", "PROMOTE", "DEMOTE", "DISPUTE", "STALE", "REFINE", "NARROW"] as const) expect(OPERATION_WORD[op]).toBeTruthy();
    for (const s of ["undetermined", "account_specific", "reusable_candidate"] as const) expect(SCOPE_WORD[s]).toBeTruthy();
  });
});

describe("KnowledgeMutations", () => {
  it("lists each mutation with a link to the knowledge object", () => {
    render(<KnowledgeMutations mutations={[mutation({ operation: "DEMOTE" }), mutation({ id: "0c17c000-0000-4000-8000-000000000099", operation: "SUPPORT" })]} />);
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    expect(within(rows[0]!).getByText("DEMOTE")).toBeTruthy();
    expect(within(rows[0]!).getByRole("link").getAttribute("href")).toBe(`/knowledge/${example.knowledge_id}`);
    expect(screen.getByRole("columnheader", { name: "Version at that time" })).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "Scope" })).toBeTruthy();
  });

  it("says an episode that changed no knowledge changed none", () => {
    render(<KnowledgeMutations mutations={[]} />);
    expect(screen.getByText("This episode changed no company knowledge.")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("reads an unreadable result as backend unavailable, not as no change", () => {
    render(<KnowledgeMutations mutations={null} />);
    expect(screen.getByText(/backend unavailable/)).toBeTruthy();
    expect(screen.queryByText(/changed no company knowledge/)).toBeNull();
  });
});
