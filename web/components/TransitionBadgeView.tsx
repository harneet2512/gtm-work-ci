import type { TransitionBadge } from "@/lib/view/transition";

export function TransitionBadgeView({ badge }: { badge: TransitionBadge }) {
  return (
    <div className={`transition-badge kind-${badge.kind}`} data-testid="transition-badge" data-kind={badge.kind}>
      <span className="kind">{badge.kind === "none" ? "No transition" : badge.kind}</span>
      <span className="detail">{badge.detail}</span>
      {badge.confidence === undefined ? null : <span className="detail">confidence {badge.confidence.toFixed(2)}</span>}
      {badge.missingRequired.length > 0 ? <span className="detail">missing: {badge.missingRequired.join(", ")}</span> : null}
    </div>
  );
}
