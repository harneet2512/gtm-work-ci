// @vitest-environment jsdom
// The episode eval page (eval design spec 4b-3), rendered from the contract-valid fixtures: the judgment
// first, the eval x option comparison, the chosen action's evals one line each with evidence, and the
// honest after-edit state. Every claim must link to evidence and no internal code may reach a main line.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { EpisodeEvals } from "@/components/evals/EpisodeEvals";
import { Verdict, VerdictCounts } from "@/components/evals/Verdict";
import { AfterEditPanel } from "@/components/evals/AfterEditPanel";
import { EvidenceList } from "@/components/evals/EvidenceList";
import { EvalLineItem } from "@/components/evals/EvalLineItem";
import { EvalMatrix } from "@/components/evals/EvalMatrix";
import { evidenceContext } from "@/lib/evals/evidence";
import { buildSelectedView } from "@/lib/evals/selected";
import { buildMatrix } from "@/lib/evals/matrix";
import type { EvalPageData } from "@/lib/load-eval-page";
import { buildAfterEdit } from "@/lib/evals/after-edit";
import { buildChain } from "@/lib/view/run-chain";
import type { AgentRun, EvalResult, HumanStrategyDecision, JudgmentInference, Knowledge, RunStrategies, RunTrace } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

afterEach(cleanup);

const trace = loadFixture<RunTrace>("acme.run-trace.json");
const strategies = loadFixture<RunStrategies>("acme.run-strategies.json");
const decision = loadExample<HumanStrategyDecision>("human_strategy_decision");
const inference = loadExample<JudgmentInference>("judgment_inference");
const knowledge = loadExample<Knowledge>("knowledge");
const RUN = trace.run.id;
const A = "0ca00000-0000-4000-8000-0000000000a1";
const noopDispute = async () => ({ ok: true as const, expected: null });

const data: EvalPageData = {
  run: trace.run as AgentRun,
  trace,
  strategies,
  decision,
  inference,
  knowledge: { [knowledge.id]: knowledge },
  notices: [],
  reevaluation: null,
};

const page = (overrides: Partial<EvalPageData> = {}, candidateId: string | null = null) =>
  render(<EpisodeEvals data={{ ...data, ...overrides }} candidateId={candidateId} dispute={noopDispute} />);

describe("Verdict", () => {
  it("is a word plus a decorative icon, never color alone", () => {
    render(<Verdict verdict="fail" />);
    expect(screen.getByText("Fail")).toBeTruthy();
    expect(document.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("keeps the word for screen readers when only the icon shows, and counts plainly", () => {
    render(<Verdict verdict="not_checked" iconOnly />);
    expect(screen.getByText("Not checked").className).toBe("sr-only");
    cleanup();
    render(<VerdictCounts counts={{ fail: 2, warn: 0, abstain: 0, pass: 1, notRelevant: 1 }} />);
    expect(screen.getByText("2 fail")).toBeTruthy();
    expect(screen.getByText("1 pass")).toBeTruthy();
    expect(screen.getByText("1 not relevant")).toBeTruthy();
    expect(screen.queryByText(/warn/)).toBeNull();
  });
});

describe("EpisodeEvals", () => {
  it("leads with the judgment on the chosen action", () => {
    page();
    const banner = screen.getByRole("region", { name: "Warn: CTA calibration" });
    expect(within(banner).getByText(/Marco said he cannot commit to a date/)).toBeTruthy();
    expect(within(banner).getByText(/Option B · Send package, buyer sets timing · chosen by Dana Kim/)).toBeTruthy();
  });

  it("compares every eval across the three options, marking Ghost's pick and the human's choice", () => {
    page();
    const table = screen.getByRole("table", { name: /Every eval Ghost ran, by option/ });
    const headers = within(table).getAllByRole("columnheader");
    expect(headers.map((h) => h.textContent)).toEqual([
      "Eval",
      expect.stringContaining("Propose a security call"),
      expect.stringContaining("Send package, buyer sets timing"),
      expect.stringContaining("Check the gate with Priya"),
    ]);
    expect(within(headers[1]!).getByText("Ghost's pick")).toBeTruthy();
    expect(within(headers[2]!).getByText("Chosen")).toBeTruthy();
    expect(within(headers[1]!).getByRole("link").getAttribute("href")).toBe(`/runs/${RUN}/evals?candidate=${A}#selected`);

    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((r) => within(r).getByRole("rowheader").textContent)).toEqual([
      expect.stringContaining("CTA calibration"),
      expect.stringContaining("Relationship continuity"),
      expect.stringContaining("Clear next step"),
      expect.stringContaining("Enough evidence"),
      expect.stringContaining("Economic buyer"),
    ]);
    const cta = within(rows[0]!).getAllByRole("cell");
    expect(within(cta[0]!).getByText("Fail")).toBeTruthy();
    expect(within(cta[0]!).getByText(/dated Tuesday ask ignores that/)).toBeTruthy();
    expect(within(cta[2]!).getByText("Not relevant")).toBeTruthy();
    expect(within(within(rows[1]!).getAllByRole("cell")[2]!).getByText("Not checked")).toBeTruthy();
  });

  it("lists the chosen action's evals worst first, each with its reason, tag, evidence and a dispute control", () => {
    page();
    const selected = screen.getByRole("region", { name: "Evals for option B: Send package, buyer sets timing" });
    const lines = within(selected).getAllByRole("listitem").filter((li) => li.classList.contains("eval-line"));
    expect(lines.map((l) => within(l).getByRole("heading", { level: 3 }).textContent)).toEqual(["CTA calibration", "Relationship continuity"]);

    const cta = lines[0]!;
    expect(cta.id).toBe("result-0e1a0000-0000-4000-8000-000000009201");
    expect(within(cta).getByText("Warn")).toBeTruthy();
    expect(within(cta).getByText(/^The draft offers a call/)).toBeTruthy();
    expect(within(cta).getByText("Sales methodology")).toBeTruthy();
    // The main line carries no grader or code; the web eval page is the expanded view, so who judged it is the
    // card's quiet meta line (grader, model, date), and the routing reason lives in the detail.
    const head = cta.querySelector(".eval-line-head")!;
    expect(head.textContent).not.toMatch(/AI judge|deepseek|cta_calibration/);
    expect((cta.querySelector(".eval-line-meta") as HTMLElement).textContent).toBe("AI judge · deepseek/deepseek-v4-flash·judged Sep 29, 2026");
    const detail = cta.querySelector("details.eval-more")!;
    expect(within(detail as HTMLElement).queryByText(/AI judge/)).toBeNull();
    expect(within(detail as HTMLElement).getByText("The draft makes an ask while the buyer has said he cannot commit to a date.")).toBeTruthy();
    // [evidence]: who, when, what was said, and a link to the source in the run trace.
    const evidence = cta.querySelector("details.eval-evidence")! as HTMLElement;
    expect(within(evidence).getByText(/Marco Ruiz/)).toBeTruthy();
    expect(within(evidence).getByText(/Sep 29, 2026/)).toBeTruthy();
    expect(within(evidence).getByRole("link", { name: /Open the email/ }).getAttribute("href")).toBe(`/runs/${RUN}#activity-0ac70000-0000-4000-8000-000000000101`);
    expect(within(cta).getByRole("button", { name: "This eval is wrong" })).toBeTruthy();

    const notRelevant = within(selected).getByText("Not relevant here (1)").closest("details")!;
    expect(within(notRelevant).getByText(/Economic buyer/)).toBeTruthy();
  });

  it("is honest that the edited email was not re-evaluated", () => {
    page();
    const after = screen.getByRole("region", { name: "Not re-evaluated yet" });
    expect(within(after).getByText("Dana Kim changed the call to action and edited a paragraph.")).toBeTruthy();
    expect(within(after).getByText(/were given on Ghost's draft, not on the edited version/)).toBeTruthy();
    expect(within(after).getByText(/Warn on CTA calibration/)).toBeTruthy();
    expect(within(after).getByText(/Sent Sep 29, 2026/)).toBeTruthy();
  });

  it("switches the detailed view to another option by URL, keeping the human's choice marked", () => {
    page({}, A);
    const selected = screen.getByRole("region", { name: "Evals for option A: Propose a security call" });
    expect(within(selected).getByText("Ghost's pick")).toBeTruthy();
    expect(within(selected).getByText(/not the option Dana Kim chose/)).toBeTruthy();
  });

  it("renders honest empty states with no strategy set", () => {
    page({ strategies: null, decision: null, notices: ["The strategy set could not be read (strategies_not_ready)."] });
    expect(screen.getByText(/strategies_not_ready/)).toBeTruthy();
    expect(screen.getByText(/No strategy set recorded for this run yet/)).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
  });
});

describe("evidence and lines when data is missing or blocking", () => {
  it("says so when a verdict cites nothing, or the trace lacks the speaker, quote or activity", () => {
    render(<EvidenceList snippets={[]} />);
    expect(screen.getByText("This verdict cites no evidence.")).toBeTruthy();
    cleanup();
    render(<EvidenceList snippets={[{ activityId: "x", quote: null, who: null, whoTitle: null, when: null, source: null, summary: null, href: null }]} />);
    expect(screen.getByText("No quote recorded for this source.")).toBeTruthy();
    expect(screen.getByText("Speaker not recorded")).toBeTruthy();
    expect(screen.getByText("The source activity is not in this run's trace.")).toBeTruthy();
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("marks a blocking failure on its line and in the matrix", () => {
    const chain = buildChain(strategies);
    const blocking = { ...chain[0]!, bundle: { ...chain[0]!.bundle!, items: chain[0]!.bundle!.items.map((i) => (i.eval_type === "champion_continuity" ? { ...i, result: { ...i.result!, blocking: true } } : i)) } };
    const view = buildSelectedView(blocking, decision, evidenceContext(trace, RUN, {}));
    const line = { ...view.lines[0]!, question: null };
    render(
      <ol>
        <EvalLineItem line={line} dispute={noopDispute} />
      </ol>,
    );
    expect(screen.getByText("Blocks send")).toBeTruthy();
    expect(screen.getByRole("heading", { level: 3 }).getAttribute("title")).toBeNull();
    cleanup();
    render(<EvalMatrix matrix={buildMatrix([blocking, ...chain.slice(1)], decision)} runId={RUN} selectedId={null} />);
    expect(screen.getByText("Blocks send")).toBeTruthy();
  });

  it("counts nothing when no eval was routed", () => {
    render(<VerdictCounts counts={{ fail: 0, warn: 0, abstain: 0, pass: 0, notRelevant: 0 }} />);
    expect(screen.getByText("No evals routed.")).toBeTruthy();
  });
});

describe("AfterEditPanel", () => {
  const chain = buildChain(strategies);
  const pending: HumanStrategyDecision = { ...decision, selected_candidate_id: A, send_decision: "pending", send_decided_at: null, human_decision_id: null };

  it("shows each verdict change after the edit", () => {
    const base = chain[1]!.bundle!.items[1]!.result!;
    const pass = { ...base, verdict: "pass", reason: "Now waits for Marco's timing." } as EvalResult;
    render(<AfterEditPanel view={buildAfterEdit(chain[1]!, { ...decision, send_decision: "pending" }, { evaluatedAt: "2026-09-29T16:04:30Z", results: [pass] })} />);
    const region = screen.getByRole("region", { name: "After the edit" });
    expect(within(region).getByText("CTA calibration WARN → PASS after your edit")).toBeTruthy();
    expect(within(region).getByText(/Re-checked Sep 29, 2026/)).toBeTruthy();
  });

  it("explains a blocked send and says how to unblock it", () => {
    const blocked = { ...chain[0]!, bundle: { ...chain[0]!.bundle!, items: chain[0]!.bundle!.items.map((i) => (i.eval_type === "champion_continuity" ? { ...i, result: { ...i.result!, blocking: true } } : i)) } };
    render(<AfterEditPanel view={buildAfterEdit(blocked, pending, null)} />);
    const note = screen.getByRole("note");
    expect(within(note).getByText("Send is blocked: Relationship continuity still fails.")).toBeTruthy();
    expect(within(note).getByText(/Priya is dropped from cc/)).toBeTruthy();
    expect(within(note).getByText(/Edit the draft so it passes, or discard it/)).toBeTruthy();
  });

  it("covers the no-decision, unedited and discarded states", () => {
    render(<AfterEditPanel view={buildAfterEdit(chain[1]!, null, null)} />);
    expect(screen.getByText(/Nobody has chosen an option yet/)).toBeTruthy();
    cleanup();
    render(<AfterEditPanel view={buildAfterEdit(chain[1]!, { ...decision, edits: [] }, null)} />);
    expect(screen.getByText(/Sent as drafted/)).toBeTruthy();
    cleanup();
    render(<AfterEditPanel view={buildAfterEdit(chain[1]!, { ...decision, send_decision: "discard" }, null)} />);
    expect(screen.getByText(/Discarded: nothing was sent/)).toBeTruthy();
  });
});
