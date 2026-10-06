// @vitest-environment jsdom
// Demo mode must survive in-page navigation: a link that drops ?demo=1 brings the operator-only Compare tab back.
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AccountIntelligencePanel } from "@/components/evals/AccountIntelligencePanel";
import { EvalOverview } from "@/components/evals/EvalOverview";
import { EvalPageHeader } from "@/components/evals/EvalPageHeader";
import { buildOverview, contractsDirFromEnv, loadEvalContracts } from "@/lib/evals/registry";

afterEach(cleanup);
const contracts = loadEvalContracts(contractsDirFromEnv({}, process.cwd()));
const overview = buildOverview(contracts, null);
const hrefs = (root: ParentNode = document) => [...root.querySelectorAll("a[href^='/evals'], a[href^='/runs'], a[href^='/accounts']")].map((a) => a.getAttribute("href")!);
const choice = { ghostPick: null, chosen: null, actor: null, agreed: false } as never;

describe("demo flag on in-page links", () => {
  it("AccountIntelligencePanel", () => {
    render(<AccountIntelligencePanel families={overview.families} demo />);
    const h = hrefs();
    expect(h.length).toBeGreaterThan(1);
    for (const href of h) expect(href).toContain("demo=1");
  });
  it("AccountIntelligencePanel without demo adds nothing", () => {
    render(<AccountIntelligencePanel families={overview.families} />);
    for (const href of hrefs()) expect(href).not.toContain("demo=1");
  });
  it("EvalOverview class filter", () => {
    render(<EvalOverview overview={overview} demo />);
    const h = hrefs();
    expect(h.length).toBeGreaterThan(1);
    for (const href of h) expect(href).toContain("demo=1");
  });
  it("EvalPageHeader", () => {
    render(<EvalPageHeader runId="r1" account="Acme" accountId="a1" decidedOn="2026-10-01T00:00:00Z" trigger={null} choice={choice} demo />);
    const h = hrefs();
    expect(h).toContain("/evals?demo=1");
    expect(h).toContain("/runs/r1?demo=1");
    expect(h).toContain("/runs?demo=1");
    expect(h).toContain("/accounts/a1?demo=1");
  });
});
