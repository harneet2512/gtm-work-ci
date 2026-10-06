import type { SendGateState } from "@/lib/evals/send-gate";

const LockIcon = () => (
  <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round">
    <rect x="3.25" y="7" width="9.5" height="6.5" rx="1.5" />
    <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
  </svg>
);

/**
 * How Send stands for an action, drawn like the Slack Send button it mirrors: locked with the reason when a
 * blocking failure stands, open otherwise. The pill is a picture of the button, not a control (the web does
 * not send); the words carry the meaning.
 */
export function SendBar({ state, title, blockers, hint }: { state: SendGateState; title: string; blockers: readonly { name: string; reason: string }[]; hint?: string }) {
  const locked = state === "blocked" || state === "would_block";
  return (
    <div className={`send-bar is-${state}`} role="note">
      <span className="send-pill" aria-hidden="true">
        {locked ? <LockIcon /> : null}
        Send
      </span>
      <div className="send-text">
        <p className="send-title">{title}</p>
        {blockers.length > 0 ? (
          <ul>
            {blockers.map((b) => (
              <li key={b.name}>
                <strong>{b.name}:</strong> {b.reason}
              </li>
            ))}
          </ul>
        ) : null}
        {hint ? <p className="hint">{hint}</p> : null}
      </div>
    </div>
  );
}
