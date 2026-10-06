/**
 * One HAR-97 placeholder section of the run trace (eval_runs, customer_reactions, knowledge_updates).
 * The contract keeps these as loose object arrays — this component never assumes a shape: empty
 * arrays get an explicit "expected from WP21/WP22" state, populated ones render each item verbatim.
 */
export function PlaceholderSection({ id, title, items, empty }: { id: string; title: string; items: readonly Record<string, unknown>[] | undefined; empty: string }) {
  return (
    <section aria-labelledby={`${id}-h`} className="panel">
      <h2 id={`${id}-h`}>{title}</h2>
      {!items || items.length === 0 ? (
        <p className="empty">{empty}</p>
      ) : (
        <ul className="eval-diffs">
          {items.map((item, i) => {
            const label =
              (typeof item.label === "string" && item.label) ||
              (typeof item.id === "string" && item.id) ||
              (typeof item.kind === "string" && item.kind) ||
              (typeof item.type === "string" && item.type) ||
              `entry ${i + 1}`;
            return (
              <li key={i}>
                <details>
                  <summary>{label}</summary>
                  <pre className="state-doc">{JSON.stringify(item, null, 2)}</pre>
                </details>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
