// @vitest-environment jsdom
// The WP24 run-chain and knowledge components, rendered from the contract-valid fixtures: the page
// answers "what did the agent decide, why, what did the human change, what did the system learn".
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { RunChain } from "@/components/run/RunChain";
import { EvalList } from "@/components/run/EvalList";
import { WhyChangedPanel } from "@/components/run/WhyChangedPanel";
import { PlaceholderSection } from "@/components/run/PlaceholderSection";
import { KnowledgeTable } from "@/components/KnowledgeTable";
import { KnowledgeDetail } from "@/components/KnowledgeDetail";
import { AccountSnapshotView } from "@/components/AccountSnapshot";
import { buildSnapshot } from "@/lib/view/account-snapshot";
import { buildChain, buildWhyChanged, type CandidateChain } from "@/lib/view/run-chain";
import { evidenceContext } from "@/lib/evals/evidence";
import { buildSelectedView } from "@/lib/evals/selected";
import { ArtifactView } from "@/components/run/ArtifactView";
import { TraceSection } from "@/components/run/TraceSection";
import type { RunPageData } from "@/lib/load-run";
import type { AccountState, Activity, HumanStrategyDecision, JudgmentInference, Knowledge, RunStrategies, RunTrace, AgentRun } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

afterEach(cleanup);

const trace = loadFixture<RunTrace>("acme.run-trace.json");
const strategies = loadFixture<RunStrategies>("acme.run-strategies.json");
const decision = loadExample<HumanStrategyDecision>("human_strategy_decision");
const inference = loadExample<JudgmentInference>("judgment_inference");
const knowledge = loadExample<Knowledge>("knowledge");
const extra = loadFixture<Knowledge>("knowledge.extra.json");

const data: RunPageData = {
  run: trace.run as AgentRun,
  trace,
  strategies,
  decision,
  inference,
  knowledge: { [knowledge.id]: knowledge },
  notices: [],
};

describe("RunChain", () => {
  it("renders the whole decision chain: run, trigger, candidates+evals, override, human decision, learned", () => {
    render(<RunChain data={data} />);
    expect(screen.getByRole("heading", { name: "Run" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "What triggered this run" })).toBeTruthy();

    // Three candidates; a1 wears the preference badge, a2 the human-choice badge.
    const candidates = screen.getAllByRole("article");
    expect(candidates).toHaveLength(3);
    const preferred = candidates[0]!;
    expect(within(preferred).getByText("Ghost's pick")).toBeTruthy();
    expect(within(preferred).queryByText("chosen by human")).toBeNull();
    const chosen = candidates[1]!;
    expect(within(chosen).getByText("chosen by human")).toBeTruthy();

    // The failed evals on the preferred draft are visible with their reasons, not collapsed to a score.
    expect(within(preferred).getByText("2 fail")).toBeTruthy();
    // In the shared eval vocabulary: plain names, the result's reason on the line, never the eval code.
    expect(within(preferred).getByText("CTA calibration")).toBeTruthy();
    expect(within(preferred).getByText("Relationship continuity")).toBeTruthy();
    expect(within(preferred).queryByText("cta_calibration")).toBeNull();
    expect(within(preferred).getByRole("link", { name: /Compare, see the evidence or dispute an eval/ }).getAttribute("href")).toBe(
      `/runs/${trace.run.id}/evals?candidate=0ca00000-0000-4000-8000-0000000000a1#selected`,
    );

    // The chain answers "why": the override, the eval-verdict diff, the semantic delta.
    const why = screen.getByRole("heading", { name: "Why did this action change?" }).closest("section")!;
    expect(within(why).getByText(/Ghost preferred/)).toBeTruthy();
    expect(within(why).getByText("Semantic delta")).toBeTruthy();
    // Knowledge citation links to the knowledge detail page.
    const cite = within(why).getByRole("link", { name: /K17/ });
    expect(cite.getAttribute("href")).toBe(`/knowledge/${knowledge.id}`);

    // The human's own record: chosen candidate, literal edits, send decision.
    const human = screen.getByRole("heading", { name: "Human decision" }).closest("section")!;
    expect(within(human).getAllByText(/Dana Kim/).length).toBeGreaterThan(0);
    expect(within(human).getByText("cta_changed")).toBeTruthy();
    expect(within(human).getByText("Send decision")).toBeTruthy();

    // The learning placeholders: one real customer reaction, two honest WP21/WP22 empties.
    expect(screen.getByRole("heading", { name: "Customer reaction" })).toBeTruthy();
    expect(screen.getByText(/No eval runs recorded here yet/)).toBeTruthy();
    expect(screen.getByText(/No knowledge update recorded yet/)).toBeTruthy();
  });

  it("renders notices and empty states when the chain is still generating", () => {
    const bare: RunPageData = {
      run: trace.run as AgentRun,
      trace: null,
      strategies: null,
      decision: null,
      inference: null,
      knowledge: {},
      notices: ["The strategy set could not be read (strategies_not_ready)."],
    };
    render(<RunChain data={bare} />);
    expect(screen.getByText(/strategies_not_ready/)).toBeTruthy();
    expect(screen.getByText(/No strategy set recorded for this run/)).toBeTruthy();
    expect(screen.getByText(/No human decision recorded/)).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "What triggered this run" })).toBeNull();
  });
});

describe("WhyChangedPanel", () => {
  it("says the decision is pending rather than inventing a comparison", () => {
    render(<WhyChangedPanel why={{ decided: false, agreed: false, preferredId: null, chosenId: null, preferredTitle: null, chosenTitle: null, preferredKnowledge: [], chosenKnowledge: [], evalDifferences: [], statement: null, semanticLabels: [], humanVerdict: null, correctedStatement: null, humanNote: null,
      evidenceRefs: [], candidateDifferences: [], noApplicableKnowledge: null, comparisonOnly: true }} knowledge={{}} />);
    expect(screen.getByText(/No human decision yet/)).toBeTruthy();
    expect(screen.getByText(/knowledge-application comparison only/)).toBeTruthy();
  });

  it("renders an agreement plainly and the single knowledge side", () => {
    const agreed = { ...buildWhyChanged(strategies, decision, inference), agreed: true };
    render(<WhyChangedPanel why={agreed} knowledge={{ [knowledge.id]: knowledge }} />);
    expect(screen.getByText(/went with Ghost's pick/)).toBeTruthy();
    expect(screen.getByText("Knowledge applied")).toBeTruthy();
    expect(screen.queryByText("Knowledge on the chosen action")).toBeNull();
  });
});

describe("EvalList and PlaceholderSection", () => {
  const chain = buildChain(strategies);
  const ctx = evidenceContext(trace, trace.run.id, {});
  const list = (c: CandidateChain) => <EvalList chain={c} view={buildSelectedView(c, decision, ctx)} runId={trace.run.id} />;

  it("a missing bundle is an explicit empty state", () => {
    render(list({ ...chain[0]!, bundle: null }));
    expect(screen.getByText("No eval bundle recorded for this candidate.")).toBeTruthy();
  });

  it("each verdict line carries the result's own reason, not the routing relevance reason", () => {
    const chosen = chain[1]!;
    const champion = chosen.bundle!.items.find((i) => i.eval_type === "champion_continuity")!;
    render(list(chosen));
    const line = screen.getByText("Relationship continuity").closest("summary")!;
    // A pass must read as a pass: its summary is the result's reason ("Priya stays on cc..."), and the
    // relevance reason (which reads like a failure) appears only in the detail as "Why this eval applies".
    expect(within(line).getByText("Pass")).toBeTruthy();
    expect(within(line).getByText(champion.result!.reason)).toBeTruthy();
    expect(within(line).queryByText(champion.relevance_reason)).toBeNull();
    const detail = line.closest("details")!;
    expect(within(detail).getByText("Why this eval applies").nextElementSibling?.textContent).toBe(champion.relevance_reason);
    expect(screen.getByText("Not relevant here (1)")).toBeTruthy();
  });

  it("says plainly when the transition policy held the candidate for review", () => {
    const restricted = {
      ...chain[0]!,
      bundle: {
        ...chain[0]!.bundle!,
        selected_eval_suite: "expansion_security",
        candidate_policy: { transition_status: "CANDIDATE" as const, status: "restricted" as const, reasons: ["expansion_motion" as const], requires_human_review: true },
      },
    };
    render(list(restricted));
    expect(screen.getByText("Held for review: an expansion ask while the account change is unconfirmed.")).toBeTruthy();
    expect(screen.queryByText(/expansion_security|expansion_motion/)).toBeNull();
  });

  it("an empty placeholder renders the expected-from-later text, a populated one renders its rows", () => {
    const { rerender } = render(<PlaceholderSection id="x" title="X" items={[]} empty="Nothing yet." />);
    expect(screen.getByText("Nothing yet.")).toBeTruthy();
    rerender(<PlaceholderSection id="x" title="X" items={[{ reaction_type: "replied", polarity: "positive" }]} empty="Nothing yet." />);
    expect(screen.queryByText("Nothing yet.")).toBeNull();
    expect(screen.getByText(/replied/)).toBeTruthy();
  });
});

describe("KnowledgeTable and KnowledgeDetail", () => {
  it("the table links each key to its detail page; the empty table says the loop writes them", () => {
    render(<KnowledgeTable items={[knowledge, extra]} />);
    const link = screen.getByRole("link", { name: "K23" });
    expect(link.getAttribute("href")).toBe(`/knowledge/${extra.id}`);
    expect(screen.getByText("provisional")).toBeTruthy();
    cleanup();
    render(<KnowledgeTable items={[]} />);
    expect(screen.getByText(/No knowledge objects yet/)).toBeTruthy();
  });

  it("the detail page renders every section: guidance, scope, evidence, lifecycle history", () => {
    render(<KnowledgeDetail k={extra} />);
    expect(screen.getByRole("heading", { name: "Guidance" })).toBeTruthy();
    expect(screen.getByText(/dated, owned next step/)).toBeTruthy();
    expect(screen.getByRole("heading", { name: "When it applies" })).toBeTruthy();
    expect(screen.getAllByText(/no meetings/).length).toBeGreaterThan(0);
    expect(screen.getByRole("heading", { name: "Evidence" })).toBeTruthy();
    expect(screen.getByText("methodology")).toBeTruthy();
    const history = screen.getByRole("heading", { name: "Lifecycle history" }).closest("section")!;
    // Newest first: the to-status badges read provisional above the initial candidate creation.
    const badges = [...history.querySelectorAll(".badge")].map((b) => b.textContent);
    expect(badges).toEqual(["provisional", "candidate"]);
  });

  it("renders the populated example: exceptions, a counterexample, provenance and evaluator users", () => {
    render(<KnowledgeDetail k={knowledge} />);
    expect(screen.getByText(knowledge.exceptions[0]!.description)).toBeTruthy();
    expect(screen.getByText(knowledge.counterexamples[0]!.note)).toBeTruthy();
    expect(screen.getByText(/signature alone scopes it/)).toBeTruthy(); // no applicability conditions
    expect(screen.getByText(knowledge.used_by_evaluators![0]!)).toBeTruthy();
    expect(screen.getByText(knowledge.provenance.note!)).toBeTruthy();
    expect(screen.getByText("supported")).toBeTruthy();
  });
});

describe("TraceSection and ArtifactView", () => {
  it("a minimal trace renders eligibility and skips the absent optional sections", () => {
    const minimal: RunTrace = {
      ...trace,
      trigger_evaluation: { ...trace.trigger_evaluation, explanation: undefined, signal_ids: [] },
      state_before: null,
      state_at_run: null,
      state_diff: null,
      signals: [],
      trigger_activities: [],
      correlated_activities: [],
      context_accesses: [],
    };
    render(<TraceSection trace={minimal} />);
    expect(screen.getByText("eligible", { exact: false })).toBeTruthy();
    expect(screen.queryByText("Signals")).toBeNull();
    expect(screen.queryByText("Activities")).toBeNull();
    expect(screen.queryByText(/context pulls/)).toBeNull();
  });

  it("the artifact view renders recipients, subject, body and attachments, and an explicit empty", () => {
    const artifact = decision.final_artifact!;
    const { rerender } = render(<ArtifactView artifact={artifact} to={decision.final_to} cc={decision.final_cc} heading="Proposed action" />);
    expect(screen.getByText("Proposed action")).toBeTruthy();
    expect(screen.getByText(/Re: Expansion to EU teams/)).toBeTruthy();
    expect(screen.getByText(/soc2-type2-2026.pdf/)).toBeTruthy();
    expect(screen.getByText(/Requested the security documents/)).toBeTruthy();
    rerender(<ArtifactView artifact={null} to={null} cc={null} />);
    expect(screen.getByText("No artifact recorded.")).toBeTruthy();
  });
});

describe("AccountSnapshotView", () => {
  const activities = loadFixture<{ items: Activity[] }>("acme.timeline.json").items;
  const state = loadExample<AccountState>("account_state");

  it("shows the milestone, the unknown commitments and the executed agent action", () => {
    render(<AccountSnapshotView snapshot={buildSnapshot(state, activities)} />);
    expect(screen.getByText("Security review of SOC2 package")).toBeTruthy();
    expect(screen.getByText("unknown")).toBeTruthy();
    expect(screen.getByText("AgentActionExecuted")).toBeTruthy();
    expect(screen.getByText(/SOC2 Type II report and pen-test summary/)).toBeTruthy();
  });

  it("lists real commitments with status and due date when the state knows them", () => {
    const snap = {
      commitments: [
        { text: "Send SOC2 Type II + pen-test summary", status: "fulfilled", dueAt: "2026-09-30T00:00:00Z" },
        { text: "Confirm EU rollout timeline", status: null, dueAt: null },
      ],
      nextMilestone: "Security review",
      latestAgentAction: null,
    };
    render(<AccountSnapshotView snapshot={snap} />);
    expect(screen.getByText("Send SOC2 Type II + pen-test summary")).toBeTruthy();
    expect(screen.getByText("fulfilled")).toBeTruthy();
    expect(screen.getByText(/due 2026-09-30/)).toBeTruthy();
    expect(screen.getByText("Confirm EU rollout timeline")).toBeTruthy();
    expect(screen.getByText("Security review")).toBeTruthy();
  });

  it("shows 'none recorded' for a known-empty commitment list", () => {
    render(<AccountSnapshotView snapshot={{ commitments: [], nextMilestone: null, latestAgentAction: null }} />);
    expect(screen.getByText("none recorded")).toBeTruthy();
  });

  it("says so when nothing is known", () => {
    render(<AccountSnapshotView snapshot={{ commitments: null, nextMilestone: null, latestAgentAction: null }} />);
    expect(screen.getAllByText("unknown").length).toBeGreaterThan(0);
    expect(screen.getByText(/No agent action in the loaded timeline window/)).toBeTruthy();
  });
});
