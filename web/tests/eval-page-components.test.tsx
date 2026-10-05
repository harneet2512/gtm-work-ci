// @vitest-environment jsdom
// The redesigned eval page on the real MedTech case: header with who chose what, the trace strip, the lead card
// with its evidence, the comparison's peeks, the cards' grader line, delta chips and the send gate.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AfterEditPanel } from "@/components/evals/AfterEditPanel";
import { EpisodeEvals } from "@/components/evals/EpisodeEvals";
import { SendBar } from "@/components/evals/SendBar";
import { TraceStrip } from "@/components/evals/TraceStrip";
import { VerdictStrip } from "@/components/evals/VerdictStrip";
import type { AgentRun, EvalResult, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace } from "@/lib/api/types";
import { buildAfterEdit } from "@/lib/evals/after-edit";
import { buildTraceSteps } from "@/lib/evals/trace-strip";
import type { EvalPageData } from "@/lib/load-eval-page";
import { buildChain } from "@/lib/view/run-chain";
import { loadFixture } from "./contract-validator";

afterEach(cleanup);

const data: EvalPageData = {
  run: loadFixture<AgentRun>("medtech.agent-run.json"),
  trace: loadFixture<RunTrace>("medtech.run-trace.json"),
  strategies: loadFixture<RunStrategies>("medtech.run-strategies.json"),
  decision: loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json"),
  inference: loadFixture<JudgmentInference>("medtech.judgment-inference.json"),
  knowledge: {},
  notices: [],
  reevaluation: null,
};
const RUN = data.run.id;
const chain = buildChain(data.strategies);
const C = chain[2]!.candidate.candidate_id;
const noop = async () => ({ ok: true as const, expected: null });
const page = (overrides: Partial<EvalPageData> = {}, candidate: string | null = null) =>
  render(<EpisodeEvals data={{ ...data, ...overrides }} candidateId={candidate} dispute={noop} />);

describe("the eval page head", () => {
  it("names the account, the decision, its trigger, and who chose what", () => {
    page();
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("MedTech Advances: how Ghost judged this decision");
    expect(screen.getByText(/after: Email from Fatoumata Touré/)).toBeTruthy();
    const crumbs = screen.getByRole("list", { name: "Breadcrumb" });
    expect(within(crumbs).getByRole("link", { name: "MedTech Advances" }).getAttribute("href")).toBe(`/accounts/${data.run.account_id}`);
    const row = document.querySelector(".choice-row") as HTMLElement;
    expect(row.textContent).toContain("Ghost's pickAOption A: Book the call, bring the cost model");
    expect(row.textContent).toContain("Chosen by Luis RodriguezBOption B: Put the costs in writing first");
  });

  it("says when the human took Ghost's pick, and when nobody has chosen", () => {
    page({ decision: { ...data.decision!, selected_candidate_id: chain[0]!.candidate.candidate_id } });
    const row = () => document.querySelector(".choice-row") as HTMLElement;
    expect(within(row()).getByText("Luis Rodriguez chose Ghost's pick")).toBeTruthy();
    cleanup();
    page({ decision: null, inference: null });
    expect(within(row()).getByText("Awaiting the human's choice")).toBeTruthy();
  });
});

describe("the eval page's degraded inputs", () => {
  it("names the trigger by its source alone when nobody sent it, and drops what the trace lacks", () => {
    const a = { ...data.trace!.trigger_activities[0]!, participants: [] };
    page({ trace: { ...data.trace!, trigger_activities: [a] } });
    expect(screen.getByText(/after: Email\. Each verdict/)).toBeTruthy();
    cleanup();
    page({ trace: null });
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("How Ghost judged this decision");
    expect(screen.queryByRole("link", { name: "MedTech Advances" })).toBeNull();
  });

  it("says Ghost recommends none when every option is blocked or held", () => {
    const strategies = { ...data.strategies!, strategy_set: { ...data.strategies!.strategy_set, no_acceptable_candidate: true } };
    page({ strategies });
    expect(screen.getByText(/Ghost recommends none of these: every option is blocked or held/)).toBeTruthy();
  });
});

describe("TraceStrip", () => {
  it("links every recorded step and marks this page as the current step", () => {
    render(<TraceStrip steps={buildTraceSteps(data)} />);
    const nav = screen.getByRole("navigation", { name: "Decision trace" });
    const links = within(nav).getAllByRole("link");
    expect(links).toHaveLength(7);
    const current = links.find((l) => l.getAttribute("aria-current") === "step")!;
    expect(current.textContent).toContain("14 verdicts");
    expect(within(nav).getByText("Email from Fatoumata Touré")).toBeTruthy();
    expect(nav.querySelectorAll("time")).toHaveLength(6);
  });

  it("renders an unrecorded step as plain text, not a dead link", () => {
    render(<TraceStrip steps={buildTraceSteps({ ...data, trace: null })} />);
    const nav = screen.getByRole("navigation", { name: "Decision trace" });
    expect(within(nav).getAllByRole("link")).toHaveLength(5);
    expect(within(nav).getAllByText("Not recorded")).toHaveLength(2);
  });
});

describe("VerdictStrip", () => {
  it("shows fail, warn and pass side by side with zeros muted, and one sentence for screen readers", () => {
    const { container } = render(<VerdictStrip counts={{ fail: 0, warn: 3, abstain: 0, pass: 2, notRelevant: 1 }} />);
    expect(screen.getByText("3 warn, 2 pass, 1 not relevant").className).toBe("sr-only");
    const segs = [...container.querySelectorAll(".strip-seg")];
    expect(segs.map((s) => s.textContent)).toEqual(["0fail", "3warn", "2pass"]);
    expect(segs[0]!.classList.contains("is-zero")).toBe(true);
    cleanup();
    render(<VerdictStrip counts={{ fail: 0, warn: 0, abstain: 1, pass: 0, notRelevant: 0 }} compact />);
    expect(screen.getByText("1 unsure")).toBeTruthy();
    cleanup();
    render(<VerdictStrip counts={{ fail: 0, warn: 0, abstain: 0, pass: 0, notRelevant: 0 }} />);
    expect(screen.getByText("No evals routed")).toBeTruthy();
  });
});

describe("the lead card and the comparison", () => {
  it("leads with the worst verdict, its reason and the words behind it", () => {
    page();
    const banner = screen.getByRole("region", { name: "Warn: Responds to the change" });
    expect(within(banner).getByText(/restates the one-time proposal prices/)).toBeTruthy();
    expect(within(banner).getByText(/long-term cost implications of integrating both the EduTech Lab/)).toBeTruthy();
    expect(within(banner).getByText("Fatoumata Touré · Nov 9, 2023 · Email")).toBeTruthy();
    expect(within(banner).getByText("Option B · Put the costs in writing first · chosen by Luis Rodriguez. 2 more verdicts to look at below.")).toBeTruthy();
  });

  it("makes each judged cell a link to its card, described by the evidence it peeks at", () => {
    page();
    const table = screen.getByRole("table", { name: /Every eval Ghost ran, by option/ });
    const failC = within(table).getAllByRole("link").find((l) => l.textContent?.startsWith("FailBlocks sendIt asks Fatoumata"))!;
    expect(failC.getAttribute("href")).toMatch(new RegExp(`^/runs/${RUN}/evals\\?candidate=${C}#result-`));
    const peek = document.getElementById(failC.getAttribute("aria-describedby")!)!;
    expect(peek.getAttribute("role")).toBe("tooltip");
    expect(peek.textContent).toBe("“Could we schedule a follow-up call to discuss these points in more detail?”Fatoumata Touré · Nov 9, 2023 · Email");
    expect(within(table).getAllByRole("columnheader")[3]!.textContent).toContain("2 fail, 1 warn, 1 pass");
  });
});

describe("cards, the send gate and the after-edit chips", () => {
  it("shows who judged each card: a rule check with no model, an AI judge with its model", () => {
    page({}, chain[1]!.candidate.candidate_id);
    const metas = [...document.querySelectorAll(".eval-line-meta")].map((m) => m.textContent);
    expect(metas).toContain("Rule check·judged Nov 9, 2023");
    expect(metas).toContain("AI judge · qwen/qwen3.8-flash·judged Nov 9, 2023");
  });

  it("locks Send on an option whose blocking failure stands, with every blocker", () => {
    page({}, C);
    const gate = within(screen.getByRole("region", { name: /Evals for option C/ })).getByRole("note");
    expect(within(gate).getByText("If chosen, Send stays blocked: Pricing policy still fails.")).toBeTruthy();
    expect(within(gate).getByText(/The extra 5% discount/)).toBeTruthy();
    expect(within(gate).getByText(/confirm the order by Nov 30/)).toBeTruthy();
  });

  it("shows Send open for a chosen option that nothing blocks, before it is sent", () => {
    const pendingA = { ...data.decision!, selected_candidate_id: chain[0]!.candidate.candidate_id, send_decision: "pending" as const, send_decided_at: null, edits: [] };
    page({ decision: pendingA, inference: null });
    const option = screen.getByRole("region", { name: /Evals for option A/ });
    expect(within(within(option).getByRole("note")).getByText("Send is open: no check blocks it.")).toBeTruthy();
  });

  it("draws each verdict change as a chip, keeping the sentence for screen readers", () => {
    const cta = chain[1]!.bundle!.items.find((i) => i.eval_type === "cta_calibration")!.result!;
    const pass = { ...cta, verdict: "pass", reason: "Now offers the two call times." } as EvalResult;
    const { container } = render(<AfterEditPanel view={buildAfterEdit(chain[1]!, { ...data.decision!, send_decision: "pending" }, { evaluatedAt: "2023-11-09T09:57:00Z", results: [pass] })} />);
    expect(screen.getByText("CTA calibration WARN → PASS after your edit").className).toBe("sr-only");
    const visual = container.querySelector(".delta-visual")!;
    expect(visual.getAttribute("aria-hidden")).toBe("true");
    expect(visual.textContent).toBe("CTA calibrationWarn→Passafter your edit");
    expect(container.querySelector(".delta-chip")!.classList.contains("to-pass")).toBe(true);
  });

  it("draws the open and blocked send bars", () => {
    const { container } = render(<SendBar state="open" title="Send is open: no check blocks it." blockers={[]} />);
    expect(container.querySelector(".send-pill svg")).toBeNull();
    cleanup();
    const blocked = render(<SendBar state="blocked" title="Send is blocked: Pricing policy still fails." blockers={[{ name: "Pricing policy", reason: "No approval." }]} hint="Edit it." />);
    expect(blocked.container.querySelector(".send-pill svg")).toBeTruthy();
    expect(screen.getByText("Edit it.")).toBeTruthy();
  });
});
