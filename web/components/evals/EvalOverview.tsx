import Link from "next/link";
import type { QualitySource } from "@/lib/evals/quality-report";
import type { EvalJob, Family, Overview } from "@/lib/evals/registry";
import type { EvidenceClass } from "@/lib/evals/vocabulary";
import { formatDay } from "@/lib/format";
import { CheckTable } from "./CheckTable";
import { FamilyBlock, StatusBar } from "./FamilyBlock";

const PERCENT = new Intl.NumberFormat("en-US", { style: "percent", maximumFractionDigits: 1 });
const pct = (v: number | null) => (v === null ? "n/a" : PERCENT.format(v));

export const JOBS: readonly { job: EvalJob; n: number; anchor: string; title: string; question: string }[] = [
  { job: "intelligence", n: 1, anchor: "job-intelligence", title: "Intelligence-building", question: "Is Ghost's understanding of the account supported by the evidence?" },
  { job: "decision_loop", n: 2, anchor: "job-decision", title: "Decision & feedback loop", question: "Is the next move right, and what did the human's judgment teach Ghost?" },
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

function Callout({ measured, source }: { measured: boolean; source: QualitySource | null }) {
  return (
    <section className={`quality-callout${measured ? " measured" : ""}`} aria-labelledby="quality-h">
      <h2 id="quality-h">Measured quality</h2>
      {measured && !source ? (
        <p>Agreement with human reviewers, false passes and false blocks below come from the eval-of-evals report. Checks it does not cover say so.</p>
      ) : source ? (
        <>
          <p>
            Agreement with gold, false passes and false blocks below come from the judge report of {formatDay(source.generatedAt)}: the {source.model} judge,{" "}
            {source.trials} trials over {source.cases} gold cases ({source.judged} judgments). Checks it does not cover say so: rule checks are tested in code and
            read <strong>Not measured yet</strong> here.
          </p>
          <p className="caveat">
            Read these numbers with two caveats. The gold is the legacy single-author set, written on invented demo accounts, not an independent human
            review. And the runtime judge is now qwen3.8-flash, which has not been measured per eval yet.
          </p>
        </>
      ) : (
        <p>
          Every eval is itself evaluated: how often it agrees with human reviewers, how often it passes something a human had to fix (false pass) and how often
          it stops a good action (false block). Those numbers come from the eval-of-evals report, which the core does not serve yet, so every check below reads{" "}
          <strong>Not measured yet</strong>. Disagreements filed with “This eval is wrong” on the eval pages feed that report.
        </p>
      )}
    </section>
  );
}

/** The evidence-class filter: links, so the URL holds the view. */
function ClassFilter({ overview, active }: { overview: Overview; active: EvidenceClass | null }) {
  const all = overview.groups.reduce((n, g) => n + g.rows.length, 0);
  return (
    <nav className="class-filter toggle" aria-label="Filter checks by how proven they are">
      <Link href="/evals#checks-h" aria-current={active === null ? "page" : undefined}>
        All <span className="num">{all}</span>
      </Link>
      {overview.groups.map((g) => (
        <Link key={g.evidenceClass} href={`/evals?class=${g.evidenceClass}#checks-h`} aria-current={active === g.evidenceClass ? "page" : undefined}>
          {g.tag.tag} <span className="num">{g.rows.length}</span>
        </Link>
      ))}
    </nav>
  );
}

/** Grader quality is a system metric: pooled agreement, false pass and false block of the judge report. */
function GraderStats({ source }: { source: QualitySource | null }) {
  return (
    <dl className="catalog-stats">
      <div className="stat">
        <dt>Judge agreement with gold</dt>
        <dd>
          <span className="stat-value num">{source ? pct(source.agreement) : "Not measured"}</span>
          <span className="stat-note">{source ? `κ ${source.kappa?.toFixed(2) ?? "n/a"} · ${source.judged} judgments` : "no eval-of-evals report yet"}</span>
        </dd>
      </div>
      <div className="stat">
        <dt>False pass · false block</dt>
        <dd>
          <span className="stat-value num">{source ? `${pct(source.falsePass)} · ${pct(source.falseBlock)}` : "Not measured"}</span>
          <span className="stat-note">{source ? "a missed problem · a stopped good action" : "no eval-of-evals report yet"}</span>
        </dd>
      </div>
    </dl>
  );
}

/**
 * The eval catalog by the three jobs of the HAR-129 demo loop: intelligence-building evals (is the understanding
 * supported?), the decision and feedback loop (the checks on every drafted action, with measured agreement where a
 * judge report covers them, and the loop beyond the action), and the system metrics, collapsed and secondary.
 * `activeClass` narrows the drafted-action checks to one evidence class.
 */
export function EvalOverview({ overview, activeClass = null }: { overview: Overview; activeClass?: EvidenceClass | null }) {
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
          They judge the step from a new event to Ghost's understanding of the account: the evidence it read, who it resolved, how the graph and state
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
        <Callout measured={overview.measured} source={overview.source} />
        <div className="panel-head">
          <h3 id="checks-h">Checks on every drafted action</h3>
          <ClassFilter overview={overview} active={activeClass} />
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
          <span className="hint">Trace integrity, grader quality, reliability, safety, tokens, latency and cost. They keep the machinery honest; they are not the loop.</span>
        </summary>
        <GraderStats source={overview.source} />
        {familiesOf(overview, "system").map((f) => (
          <FamilyBlock key={f.id} family={f} />
        ))}
      </details>
    </>
  );
}
