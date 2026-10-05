// @vitest-environment jsdom
// Cliff mode (HAR-145): the receipt state is never conflated - unreadable, not applicable, not reserved,
// reserved-pending and posted each say so, beside a card built from the real payload.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { CliffBody } from "@/components/episode/CliffBody";
import type { BusinessIntelligence, HumanStrategyDecision, JudgmentInference, RunStrategies, SurfaceMessage } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

afterEach(cleanup);

const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const inference = loadFixture<JudgmentInference>("medtech.judgment-inference.json");
const bi = loadExample<BusinessIntelligence>("business_intelligence_update");
const ref = loadExample<SurfaceMessage>("surface_message");
const posted = (kind: string): SurfaceMessage => ({ ...ref, kind: kind as SurfaceMessage["kind"] });
const pending = (kind: string): SurfaceMessage => ({ ...posted(kind), ts: null });

const row = (n: number) => document.querySelectorAll<HTMLElement>(".msg-row")[n - 1]!;
const base = { runId: "0f0aad00-0000-4000-8000-00000000ad60", readable: true, bi: null, biStatus: "none" as const, strategies: null, decision: null, inference: null };

describe("CliffBody receipts", () => {
  it("says 'not applicable' for a kind that was never queried", () => {
    render(<CliffBody {...base} surfaces={{}} />);
    for (const n of [1, 2, 3]) expect(row(n).textContent).toContain("Not applicable");
  });

  it("says 'not observable' for unqueried kinds when the surface read failed", () => {
    render(<CliffBody {...base} readable={false} surfaces={{}} />);
    for (const n of [1, 2, 3]) expect(row(n).textContent).toContain("not observable");
    expect(document.body.textContent).not.toContain("Not applicable");
  });

  it("says 'not reserved' for a null ref", () => {
    render(<CliffBody {...base} surfaces={{ bi: null, chooser: null, judgment: null }} />);
    for (const n of [1, 2, 3]) expect(row(n).textContent).toContain("Not reserved");
  });

  it("distinguishes reserved-pending from posted and strips a leading # from the channel", () => {
    render(<CliffBody {...base} surfaces={{ bi: posted("bi"), chooser: pending("chooser"), judgment: { ...posted("judgment"), channel: "#ghost" } }} />);
    expect(row(1).textContent).toContain("posted");
    expect(row(1).textContent).toContain(ref.ts as string);
    expect(row(2).textContent).toContain("ts pending");
    expect(row(2).textContent).not.toContain("posted");
    expect(row(3).querySelector("code")!.textContent).toBe("#ghost");
  });
});

describe("CliffBody cards", () => {
  it("renders empty-state copy when no payload exists", () => {
    render(<CliffBody {...base} surfaces={{}} />);
    expect(row(1).textContent).toContain("No business-intelligence update on this account.");
    expect(row(2).textContent).toContain("No strategy set on this run.");
    expect(row(3).textContent).toContain("No judgment inference on this episode.");
  });

  it("says Message 1 is not resolvable when the episode's account change cannot be matched", () => {
    render(<CliffBody {...base} biStatus="unresolvable" surfaces={{}} />);
    expect(row(1).textContent).toContain("Message 1 not resolvable for this episode.");
    expect(row(1).textContent).not.toContain("No business-intelligence update on this account.");
  });

  it("renders M1 from the BI update with claim count and transition", () => {
    render(<CliffBody {...base} biStatus="matched" bi={{ ...bi, transition: { from: "a", to: "b" } as never }} surfaces={{ bi: posted("bi") }} />);
    expect(within(row(1)).getByText(bi.summary)).toBeTruthy();
    expect(row(1).textContent).toContain(`${bi.claims.length} claims`);
    expect(row(1).textContent).toContain("transition");
    expect(within(row(1)).getByRole("link").getAttribute("href")).toBe(`/accounts/${bi.account_id}#intelligence-evals`);
  });

  it("omits the why line and the transition when the BI update has neither", () => {
    render(<CliffBody {...base} biStatus="matched" bi={{ ...bi, why_it_matters: "", transition: null, claims: undefined as never }} surfaces={{}} />);
    expect(row(1).querySelector(".m-why")).toBeNull();
    expect(row(1).textContent).toContain("0 claims");
    expect(row(1).textContent).not.toContain("transition");
  });

  it("renders M2 as lettered options with Ghost's pick and the human's choice marked", () => {
    render(<CliffBody {...base} strategies={strategies} decision={decision} surfaces={{ chooser: posted("chooser") }} />);
    const n = strategies.strategy_set!.candidates.length;
    expect(row(2).textContent).toContain(`gtm_ai drafted ${n} moves`);
    expect(row(2).querySelectorAll(".m-options li")).toHaveLength(n);
    expect(row(2).querySelectorAll("li.chosen")).toHaveLength(1);
    expect(row(2).textContent).toContain("Ghost's pick");
    expect(within(row(2)).getByRole("link").getAttribute("href")).toBe(`/runs/${base.runId}/evals`);
  });

  it("renders M2 with no chosen option when no human decision exists", () => {
    render(<CliffBody {...base} strategies={strategies} decision={null} surfaces={{}} />);
    expect(row(2).querySelectorAll("li.chosen")).toHaveLength(0);
  });

  it("renders M3 from the judgment inference and links to the chosen option's evals", () => {
    render(<CliffBody {...base} decision={decision} inference={inference} surfaces={{ judgment: posted("judgment") }} />);
    expect(row(3).textContent).toContain(`The human ${inference.agreement} Ghost's pick`);
    expect(within(row(3)).getByRole("link").getAttribute("href")).toBe(`/runs/${base.runId}/evals?candidate=${decision.selected_candidate_id}`);
  });

  it("links M3 evidence without a candidate when there is no decision", () => {
    render(<CliffBody {...base} inference={inference} surfaces={{}} />);
    expect(within(row(3)).getByRole("link").getAttribute("href")).toBe(`/runs/${base.runId}/evals?candidate=`);
  });

  it("labels the section for assistive tech", () => {
    render(<CliffBody {...base} surfaces={{}} />);
    expect(screen.getByLabelText("Cliff messages")).toBeTruthy();
  });
});
