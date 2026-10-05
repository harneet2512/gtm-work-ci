import Link from "next/link";
import type { Family } from "@/lib/evals/registry";
import { withDemo } from "@/lib/view/demo-link";

/**
 * Job 1 on the account map (HAR-129 demo-loop clarification), where Slack Message 1's "View evals" lands: the
 * intelligence-building evals judge exactly what this page shows (claims, graph and state diff). Per-change
 * verdicts are not served by the core yet, so the panel names the evals and their build status instead of verdicts.
 */
export function AccountIntelligencePanel({ families, demo = false }: { families: readonly Family[]; demo?: boolean }) {
  return (
    <section id="intelligence-evals" className="panel job-band is-intelligence" aria-labelledby="intelligence-evals-h">
      <p className="eyebrow">Job 1 · Intelligence-building evals</p>
      <h2 id="intelligence-evals-h">How Ghost's understanding is checked</h2>
      <p className="band-lead">
        The claims, the graph and its diff on this page are what these evals judge: is every fact sourced, is every person the right one, did the change
        touch only what the evidence supports, and does the company knowledge apply here?
      </p>
      <div className="band-status" role="note">
        <p>
          <strong>Per-change verdicts are not shown on this page.</strong> The evals below judge these claims, and their counts for each run are under Results in
          Evals. None are guessed here.
        </p>
        <ul className="family-chips">
          {families.map((f) => (
            <li key={f.id}>
              <Link href={withDemo(`/evals#family-${f.id}`, demo)}>{f.name}</Link> <span className="hint">{f.counts.live} live</span>
            </li>
          ))}
          <li>
            <Link href={withDemo("/evals#job-intelligence", demo)}>All intelligence-building evals</Link>
          </li>
        </ul>
      </div>
    </section>
  );
}
