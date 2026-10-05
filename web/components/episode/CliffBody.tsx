import Link from "next/link";
import type { BiStatus } from "@/lib/load-episode";
import type { BusinessIntelligence, HumanStrategyDecision, JudgmentInference, RunStrategies, SurfaceMessage } from "@/lib/api/types";

/**
 * Cliff mode (HAR-145): the Slack surface beside its evidence. Each of the three messages gets a card
 * rendered from the same contract payloads the Slack renderer consumes (BI update / strategy set /
 * judgment inference), next to the write-once posting receipt and a link into the eval evidence.
 * A kind with no ref says "not reserved" — the absence is information, not an error.
 */
export function CliffBody({
  surfaces,
  bi,
  biStatus,
  strategies,
  decision,
  inference,
  runId,
  readable,
}: {
  surfaces: Partial<Record<"bi" | "chooser" | "judgment", SurfaceMessage | null>>;
  /** False when the surface read threw — an absent key then means unreadable, not inapplicable. */
  readable: boolean;
  bi: BusinessIntelligence | null;
  /** matched: BI update for this episode's account change; none: the account has none; unresolvable: cannot be tied to this episode. */
  biStatus: BiStatus;
  strategies: RunStrategies | null;
  decision: HumanStrategyDecision | null;
  inference: JudgmentInference | null;
  runId: string;
}) {
  return (
    <section className="cliff" aria-label="Cliff messages">
      <MsgRow
        title="Message 1 — business intelligence"
        receipt={surfaces.bi}
        unreadable={!readable && surfaces.bi === undefined}
        evidence={<Link href={`/accounts/${bi?.account_id ?? ""}#intelligence-evals`}>Account intelligence evals</Link>}
        empty={bi ? null : biStatus === "unresolvable" ? "Message 1 not resolvable for this episode." : "No business-intelligence update on this account."}
      >
        {bi ? (
          <div className="slack-card">
            <p className="m-title">{bi.summary}</p>
            {bi.why_it_matters ? <p className="m-why">{bi.why_it_matters}</p> : null}
            <p className="m-meta">
              {(bi.claims ?? []).length} claims{bi.transition ? ` · transition ${JSON.stringify(bi.transition).slice(0, 80)}` : ""}
            </p>
          </div>
        ) : null}
      </MsgRow>

      <MsgRow
        title="Message 2 — strategy chooser"
        receipt={surfaces.chooser}
        unreadable={!readable && surfaces.chooser === undefined}
        evidence={<Link href={`/runs/${runId}/evals`}>Run evals</Link>}
        empty={strategies?.strategy_set?.candidates?.length ? null : "No strategy set on this run."}
      >
        {strategies?.strategy_set?.candidates?.length ? (
          <div className="slack-card">
            <p className="m-title">gtm_ai drafted {strategies.strategy_set.candidates.length} moves</p>
            <ul className="m-options">
              {strategies.strategy_set.candidates.map((c, i) => (
                <li key={c.candidate_id} className={c.candidate_id === decision?.selected_candidate_id ? "chosen" : ""}>
                  <span className="mono">{String.fromCharCode(65 + i)}</span> {c.title ?? c.strategy_type}
                  {c.preferred_by_agent ? <span className="tag"> Ghost's pick</span> : null}
                  {c.candidate_id === decision?.selected_candidate_id ? <span className="tag"> chosen</span> : null}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </MsgRow>

      <MsgRow
        title="Message 3 — what the judgment taught"
        receipt={surfaces.judgment}
        unreadable={!readable && surfaces.judgment === undefined}
        evidence={<Link href={`/runs/${runId}/evals?candidate=${decision?.selected_candidate_id ?? ""}`}>The chosen option's evals</Link>}
        empty={inference ? null : "No judgment inference on this episode."}
      >
        {inference ? (
          <div className="slack-card">
            <p className="m-title">
              The human {inference.agreement} Ghost's pick
            </p>
            <p className="m-why">{inference.agent_preference} → {inference.human_choice}</p>
          </div>
        ) : null}
      </MsgRow>
    </section>
  );
}

/** One message row: the rendered card beside the posting receipt and the evidence link. */
function MsgRow({
  title,
  receipt,
  unreadable,
  evidence,
  empty,
  children,
}: {
  title: string;
  receipt: SurfaceMessage | null | undefined;
  unreadable: boolean;
  evidence: React.ReactNode;
  empty: string | null;
  children: React.ReactNode;
}) {
  const ref = receipt;
  return (
    <div className="msg-row">
      <div className="msg-side">
        <p className="eyebrow">{title}</p>
        {unreadable ? (
          <p className="hint">Surface ref not observable on this core.</p>
        ) : ref === undefined ? (
          <p className="hint">Not applicable — nothing exists for this message to carry.</p>
        ) : ref === null ? (
          <p className="hint">Not reserved — this message was never queued for Slack.</p>
        ) : ref.ts != null ? (
          <p className="receipt">
            <span className="tag post">posted</span> <code>#{ref.channel.replace(/^#/, "")}</code> · ts <code>{ref.ts}</code> · reserved {ref.reserved_at.slice(0, 19)}
          </p>
        ) : (
          <p className="receipt">
            <span className="tag">reserved</span> <code>#{ref.channel.replace(/^#/, "")}</code> · ts pending — a crash between post and write is reconciled against the channel.
          </p>
        )}
        <p className="hint">{evidence}</p>
      </div>
      <div className="msg-card">{empty ? <p className="hint">{empty}</p> : children}</div>
    </div>
  );
}
