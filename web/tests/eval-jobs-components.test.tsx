// @vitest-environment jsdom
// The three eval jobs on the run eval page (HAR-129 demo-loop clarification): job 1 shows what gtm_ai understood from
// Event N with its evidence and says plainly that per-event verdicts are not shown there; job 2 is the decision, followed
// by the knowledge trace; job 3 is a collapsed system panel built only from what the run recorded.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { EpisodeEvals } from "@/components/evals/EpisodeEvals";
import { IntelligenceSection } from "@/components/evals/IntelligenceSection";
import { KnowledgeTraceSection } from "@/components/evals/KnowledgeTraceSection";
import { SystemPanel } from "@/components/evals/SystemPanel";
import type { AgentRun, HumanStrategyDecision, JudgmentInference, Knowledge, RunStrategies, RunTrace } from "@/lib/api/types";
import { evidenceContext } from "@/lib/evals/evidence";
import { buildKnowledgeTrace } from "@/lib/evals/knowledge-trace";
import { buildOverview, loadEvalContracts } from "@/lib/evals/registry";
import { buildSystemView } from "@/lib/evals/system-view";
import { buildUnderstanding } from "@/lib/evals/understanding";
import type { EvalPageData } from "@/lib/load-eval-page";
import { CONTRACTS_DIR, loadExample, loadFixture } from "./contract-validator";

afterEach(cleanup);

const medtech: EvalPageData = {
  run: loadFixture<AgentRun>("medtech.agent-run.json"),
  trace: loadFixture<RunTrace>("medtech.run-trace.json"),
  strategies: loadFixture<RunStrategies>("medtech.run-strategies.json"),
  decision: loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json"),
  inference: loadFixture<JudgmentInference>("medtech.judgment-inference.json"),
  knowledge: {},
  notices: [],
  reevaluation: null,
};
const families = buildOverview(loadEvalContracts(CONTRACTS_DIR), null).families.filter((f) => f.job === "intelligence");
const understanding = buildUnderstanding(medtech.trace, evidenceContext(medtech.trace, medtech.run.id, {}));

describe("job 1: what gtm_ai understood from Event N", () => {
  it("shows each material change with what it added and the customer's words", () => {
    render(<IntelligenceSection understanding={understanding} families={families} />);
    const section = screen.getByRole("region", { name: "What gtm_ai understood from Event N" });
    expect(within(section).getByRole("link", { name: "Email from Fatoumata Touré, Nov 9, 2023" })).toBeTruthy();
    expect(within(section).getByText(/moved the account state from v107 to v108/)).toBeTruthy();
    const items = section.querySelectorAll(".understood");
    expect(items).toHaveLength(5);
    expect(items[0]!.querySelector(".is-added")!.textContent).toBe("Added: Long-term cost of integrating EduTech Lab and SecureData Nexus");
    expect(within(items[2] as HTMLElement).getByText("Inferred from the customer's words · 90%")).toBeTruthy();
    expect(items[2]!.querySelector(".was")!.textContent).toBe("unknown");
  });

  it("says plainly that verdicts on this understanding are not shown here, and lists the evals that would give them", () => {
    render(<IntelligenceSection understanding={understanding} families={families} />);
    const note = screen.getByRole("note");
    expect(note.textContent).toMatch(/^Verdicts on this understanding are not shown here./);
    expect(within(note).getAllByRole("link").map((a) => a.getAttribute("href"))).toEqual(families.map((f) => `/evals#family-${f.id}`));
  });

  it("degrades honestly without a trace or a material change", () => {
    render(<IntelligenceSection understanding={null} families={[]} />);
    expect(screen.getByText("The run trace is not recorded, so what changed cannot be shown.")).toBeTruthy();
    cleanup();
    render(<IntelligenceSection understanding={{ ...understanding!, changes: [], signals: [], bookkeeping: 0, version: null, event: { ...understanding!.event, who: null } }} families={[]} />);
    expect(screen.getByText("The run's state diff records no material change.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Email, Nov 9, 2023" })).toBeTruthy();
  });
});

describe("job 2: company knowledge in this decision", () => {
  it("says when no knowledge was retrieved, used or decisive", () => {
    render(<KnowledgeTraceSection trace={buildKnowledgeTrace(medtech)} />);
    const s = screen.getByRole("region", { name: "Company knowledge in this decision" });
    expect(within(s).getByText("Not recorded: this run kept no record of the company knowledge it read.")).toBeTruthy();
    expect(within(s).getByText("No option cited company knowledge.")).toBeTruthy();
    expect(within(s).getByText("No: the chosen option cites no company knowledge.")).toBeTruthy();
  });

  it("keeps retrieved, applicable, cited and influence as separate steps and never claims the decision changed", () => {
    render(<KnowledgeTraceSection trace={buildKnowledgeTrace(medtech)} />);
    const s = screen.getByRole("region", { name: "Company knowledge in this decision" });
    const steps = [...s.querySelectorAll(".k-step")].map((e) => e.textContent);
    expect(steps).toEqual(["Retrieved", "Applicable", "Cited", "In chosen option", "Influence"]);
    expect(within(s).getByText("Not recorded: no applicability record exists for this run.")).toBeTruthy();
    expect(within(s).getByText("Not measured: citing knowledge is not evidence that it changed the decision.")).toBeTruthy();
    expect(s.textContent).not.toContain("Changed the decision");
  });

  it("follows used knowledge to the human's choice and the inference", () => {
    const k17 = loadExample<Knowledge>("knowledge");
    const trace = loadFixture<RunTrace>("acme.run-trace.json");
    const acme = { ...medtech, run: trace.run as AgentRun, trace, strategies: loadFixture<RunStrategies>("acme.run-strategies.json"), decision: loadExample<HumanStrategyDecision>("human_strategy_decision"), inference: loadExample<JudgmentInference>("judgment_inference"), knowledge: { [k17.id]: k17 } };
    const attribution = { asOf: "2023-11-09T12:00:00Z", retrieved: 3, applicable: 2, exceptionBlocked: 1, used: 1 };
    render(<KnowledgeTraceSection trace={{ ...buildKnowledgeTrace(acme), retrievedTraced: true, attribution }} />);
    expect(screen.getByRole("link", { name: /^K17:/ }).getAttribute("href")).toBe(`/knowledge/${k17.id}`);
    expect(screen.getByText(/by option B/)).toBeTruthy();
    expect(screen.getByText("Yes: the human chose an option that cites it, and gtm_ai's inference cites it. This is not a measure of influence.")).toBeTruthy();
    expect(screen.getByText("3 pieces of company knowledge read as of Nov 9, 2023.")).toBeTruthy();
    expect(screen.getByText("2 judged applicable, 1 set aside by an exception.")).toBeTruthy();
  });
});

describe("job 3: the system panel", () => {
  it("is collapsed and shows trace checks, step times, model and judge figures only as recorded", () => {
    const { container } = render(<SystemPanel view={buildSystemView(medtech)} />);
    const panel = container.querySelector("details.run-system") as HTMLDetailsElement;
    expect(panel.open).toBe(false);
    expect([...panel.querySelectorAll(".sys-checks li")].map((li) => li.className)).toEqual(Array(6).fill("is-ok"));
    expect(within(panel).getByText("Await the human").nextElementSibling?.textContent).toBe("27 min succeeded");
    expect(within(panel).getByText("qwen/qwen3.8-flash")).toBeTruthy();
    expect(within(panel).getByText("14 (2 rule checks, 12 AI judge)")).toBeTruthy();
    expect(within(panel).getAllByText("not recorded")).toHaveLength(3);
  });

  it("formats recorded judge cost, latency and tokens", () => {
    render(<SystemPanel view={{ ...buildSystemView(medtech), steps: [{ step: "custom", status: "ok", seconds: null }], judges: { verdicts: 1, rule: 0, ai: 1, costUsd: 0.0012, latencyMs: 2100, tokens: 2406 }, model: null }} />);
    expect(screen.getByText("$0.0012")).toBeTruthy();
    expect(screen.getByText("2,100 ms total")).toBeTruthy();
    expect(screen.getByText("2,406")).toBeTruthy();
    expect(screen.getByText("custom")).toBeTruthy();
  });
});

describe("the eval page keeps the jobs in loop order", () => {
  it("renders job 1, then the decision, then the knowledge trace, then the system panel", () => {
    const { container } = render(<EpisodeEvals data={medtech} candidateId={null} dispute={async () => ({ ok: true, expected: null })} intelligenceFamilies={families} />);
    const name = (e: Element) => (e.id ? e.id : ["verdict-banner", "knowledge-trace", "run-system"].find((c) => e.classList.contains(c)));
    const order = [...container.querySelectorAll("#intelligence, #decision, .verdict-banner, .knowledge-trace, .run-system")].map(name);
    expect(order).toEqual(["intelligence", "decision", "verdict-banner", "knowledge-trace", "run-system"]);
  });
});
