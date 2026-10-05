import Link from "next/link";
import type { Family } from "@/lib/evals/registry";

/**
 * Job 1 on the account map (HAR-129 demo-loop clarification), where Slack Message 1's "View evals" lands: the
 * intelligence-building evals judge exactly what this page shows (claims, graph and state diff). Per-change
 * verdicts are not served by the core yet, so the panel names the evals and their build status instead of verdicts.
 */
export function AccountIntelligencePanel({ families }: { families: readonly Family[] }) {
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
          <strong>Per-change verdicts are not served by the core yet.</strong> The live evals run in the core, but no endpoint returns their results for one
          change, so none are shown here rather than guessed.
        </p>
        <ul className="family-chips">
          {families.map((f) => (
            <li key={f.id}>
              <Link href={`/evals#family-${f.id}`}>{f.name}</Link> <span className="hint">{f.counts.live} live</span>
            </li>
          ))}
          <li>
            <Link href="/evals#job-intelligence">All intelligence-building evals</Link>
          </li>
        </ul>
      </div>
    </section>
  );
}
