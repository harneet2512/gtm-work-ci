// @vitest-environment jsdom
// The account map's intelligence-evals panel: where Slack Message 1's "View evals" lands. It names the job-1 evals
// that judge this page's claims, graph and state diff, links each to the catalog, and is honest that per-change
// verdicts are not shown on the page.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AccountIntelligencePanel } from "@/components/evals/AccountIntelligencePanel";
import { loadJobFamilies } from "@/lib/evals/job-families";
import { CONTRACTS_DIR } from "./contract-validator";

afterEach(cleanup);
const env = { GHOST_CONTRACTS_DIR: CONTRACTS_DIR };

describe("loadJobFamilies", () => {
  it("reads one job's families from the registry", () => {
    expect(loadJobFamilies("intelligence", env, "/nowhere").map((f) => f.id)).toEqual(["E1", "E2", "E3", "E4", "E5", "E6", "E7"]);
    expect(loadJobFamilies("system", env, "/nowhere")).toHaveLength(5);
  });

  it("returns none when the contracts cannot be read", () => {
    expect(loadJobFamilies("intelligence", { GHOST_CONTRACTS_DIR: "/nowhere" }, "/nowhere")).toEqual([]);
  });
});

describe("AccountIntelligencePanel", () => {
  it("is the anchor Message 1 links to, names the evals and says verdicts are not shown on this page", () => {
    const { container } = render(<AccountIntelligencePanel families={loadJobFamilies("intelligence", env, "/nowhere")} />);
    const panel = container.querySelector("#intelligence-evals")!;
    expect(within(panel as HTMLElement).getByRole("heading", { level: 2 }).textContent).toBe("How gtm_ai's understanding is checked");
    expect(panel.textContent).toMatch(/Per-change verdicts are not shown on this page/);
    const links = within(panel as HTMLElement).getAllByRole("link");
    expect(links[0]!.getAttribute("href")).toBe("/evals?view=catalog#family-E1");
    expect(links.at(-1)!.getAttribute("href")).toBe("/evals?view=catalog#job-intelligence");
  });

  it("still explains itself when the registry could not be read", () => {
    render(<AccountIntelligencePanel families={[]} />);
    expect(screen.getByText(/Per-change verdicts are not shown on this page/)).toBeTruthy();
    expect(screen.getAllByRole("link")).toHaveLength(1);
  });
});
