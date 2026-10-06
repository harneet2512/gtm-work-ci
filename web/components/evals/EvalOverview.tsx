import Link from "next/link";
import type { EvalJob, Family, Overview } from "@/lib/evals/registry";
import type { EvidenceClass } from "@/lib/evals/vocabulary";
import { withDemo } from "@/lib/view/demo-link";
import { CheckTable } from "./CheckTable";
import type { GateResultRow } from "@/lib/evals/bucket2-results";
import { DecisionLearningView } from "./DecisionLearningView";
import { FamilyBlock, StatusBar } from "./FamilyBlock";
import { JudgeQuality } from "./JudgeQuality";

export const JOBS: readonly { job: EvalJob; n: number; anchor: string; title: string; question: string }[] = [
  { job: "intelligence", n: 1, anchor: "job-intelligence", title: "Intelligence-building", question: "Is gtm_ai's understanding of the account supported by the evidence?" },
  { job: "decision_loop", n: 2, anchor: "job-decision", title: "Decision & feedback loop", question: "Is the next move right, and what did the human's judgment teach gtm_ai?" },
  { job: "system", n: 3, anchor: "job-system", title: "System", question: "Is the machinery sound: traces, graders, reliability, cost?" },
];

const familiesOf = (o: Overview, job: EvalJob) => o.families.filter((f) => f.job === job);
const tally = (fs: readonly Family[]) =>
  fs.reduce((t, f) => ({ live: t.live + f.counts.live, partial: t.partial + f.counts.partial, planned: t.planned + f.counts.planned }), { live: 0, partial: 0, planned: 0 });

/** The three jobs side by side, each a link to its section: what it asks, how much of it exists. */
function JobNav({ overview }: { overview: Overview }) {
  return (
    <nav className="job-nav" aria-label="The three eval jobs">
      {JOBS.map((j) => {
        const fs = familiesOf(overview, j.job);
        const t = tally(fs);
        const total = t.live + t.partial + t.planned;
        return (
          <a key={j.job} href={`#${j.anchor}`} className={`job-tile is-${j.job}`}>
            <span className="job-num">{j.n}</span>
            <span className="job-title">{j.title}</span>
            <span className="job-question">{j.question}</span>
            <StatusBar counts={t} />
            <span className="job-counts">
              {total} evals in {fs.length} families · {t.live} live
              {j.job === "decision_loop" ? ` · plus ${overview.totals.draftTypes} checks on every drafted action` : ""}
            </span>
          </a>
        );
      })}
    </nav>
  );
}

/** The evidence-class filter: links, so the URL holds the view. */
function ClassFilter({ overview, active, demo }: { overview: Overview; active: EvidenceClass | null; demo: boolean }) {
  const all = overview.groups.reduce((n, g) => n + g.rows.length, 0);
  return (
    <nav className="class-filter toggle" aria-label="Filter checks by how proven they are">
      <Link href={withDemo("/evals?view=catalog#checks-h", demo)} aria-current={active === null ? "page" : undefined}>
        All <span className="num">{all}</span>
      </Link>
      {overview.groups.map((g) => (
        <Link key={g.evidenceClass} href={withDemo(`/evals?view=catalog&class=${g.evidenceClass}#checks-h`, demo)} aria-current={active === g.evidenceClass ? "page" : undefined}>
          {g.tag.tag} <span className="num">{g.rows.length}</span>
        </Link>
      ))}
    </nav>
  );
}

/**
 * The eval catalog by the three jobs of the demo loop: intelligence-building evals (is the understanding supported?),
 * the decision and feedback loop (the checks on every drafted action and the loop beyond the action), and the system
 * metrics, collapsed and secondary, which also hold judge quality. `activeClass` narrows the drafted-action checks to
 * one evidence class.
 */
export function EvalOverview({
  overview,
  activeClass = null,
  demo = false,
  gateResults = [],
  gateEpisodeId = null,
}: {
  overview: Overview;
  activeClass?: EvidenceClass | null;
  demo?: boolean;
  /** The stored gate results of one episode (Bucket 2 rows); none reads "Not measured". */
  gateResults?: readonly GateResultRow[];
  gateEpisodeId?: string | null;
}) {
  const groups = activeClass ? overview.groups.filter((g) => g.evidenceClass === activeClass) : overview.groups;
  const t = overview.totals;
  return (
    <>
      <p className="lead">
        {t.registryEvals} evals in {overview.families.length} families, by the job they do: {t.live} live, {t.partial} partial, {t.planned} planned.
      </p>
      <JobNav overview={overview} />

      <section id="job-intelligence" className="panel job-section" aria-labelledby="job-intelligence-h">
        <p className="eyebrow">Job 1</p>
        <h2 id="job-intelligence-h">Intelligence-building evals</h2>
        <p className="lead">
          They judge the step from a new event to gtm_ai's understanding of the account: the evidence it read, who it resolved, how the graph and state
          changed, and which company knowledge applies. They sit with the business-intelligence update and the state diff, before any strategy exists.
        </p>
        {familiesOf(overview, "intelligence").map((f) => (
          <FamilyBlock key={f.id} family={f} />
        ))}
      </section>

      <section id="job-decision" className="panel job-section" aria-labelledby="job-decision-h">
        <p className="eyebrow">Job 2</p>
        <h2 id="job-decision-h">Decision & feedback-loop evals</h2>
        <p className="lead">
          They judge the step from that understanding to a decision, the action, the human's choice and edit, and the knowledge it leaves behind for later
          episodes.
        </p>
        <DecisionLearningView buckets={overview.buckets} bucket2Results={gateResults} episodeId={gateEpisodeId} />
        <div className="panel-head">
          <h3 id="checks-h">Checks on every drafted action</h3>
          <ClassFilter overview={overview} active={activeClass} demo={demo} />
        </div>
        <p className="lead">
          {t.draftTypes} checks can judge a drafted action. Only the ones that apply to the account's state and to the action run, so a decision shows a
          handful of them, each with its verdict and reason.
        </p>
        {groups.map((g) => (
          <CheckTable key={g.evidenceClass} group={g} />
        ))}
        <h3 className="families-h">The loop beyond the drafted action</h3>
        {familiesOf(overview, "decision_loop").map((f) => (
          <FamilyBlock key={f.id} family={f} />
        ))}
      </section>

      <details id="job-system" className="panel job-section system-panel">
        <summary>
          <span className="eyebrow">Job 3</span>
          <span className="system-title">System metrics</span>
          <span className="hint">Trace integrity, judge quality, reliability, safety, tokens, latency and cost. They keep the machinery honest; they are not the loop.</span>
        </summary>
        <JudgeQuality overview={overview} />
        {familiesOf(overview, "system").map((f) => (
          <FamilyBlock key={f.id} family={f} />
        ))}
        <section className="metrics-list" aria-labelledby="metrics-h">
          <h3 id="metrics-h">Metrics</h3>
          <p className="hint">Tokens, tool use, eval overhead, latency and cost are measurements of the machinery. They are not evals and are counted in none of the totals above.</p>
          <ul>
            {overview.metrics.map((m) => (
              <li key={m.id}>{m.name}</li>
            ))}
          </ul>
        </section>
      </details>
    </>
  );
}
