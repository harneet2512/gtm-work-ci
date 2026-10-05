import { firstParam } from "@/lib/params";
import { core } from "@/lib/api/server";
import { CoreError } from "@/lib/api/core-client";
import { systemView, type BandStatus } from "@/lib/view/system";
import { formatUtc } from "@/lib/format";
import { ControlManifestForm } from "@/components/control/ControlManifestForm";
import type { InvisibilityReport, OutboxEvent, ProviderBreakerStatus } from "@/lib/api/types";
import { ScrollTable } from "@/components/ScrollTable";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;

const mark = (s: BandStatus): string => (s === "ok" ? "✓" : s === "warn" ? "⚠" : s === "bad" ? "✗" : "?");

/**
 * /system — the platform-health surface (HAR-145): is the core up, is the provider breaker holding
 * model calls, is the surface outbox draining, and did the held-out event stay invisible. Every row
 * is a real core read; a failed read shows as unknown/unreadable, never as green.
 */
export default async function SystemPage({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  const manifestId = firstParam(sp.manifest)?.trim().toLowerCase() ?? null;

  const api = core();
  let coreOk = false;
  try {
    coreOk = (await api.getHealth()).status === "ok";
  } catch {
    coreOk = false;
  }
  const read = async <T,>(f: () => Promise<T>): Promise<T | null> => {
    try {
      return await f();
    } catch (e) {
      if (e instanceof CoreError && e.status === 404) return null;
      return null;
    }
  };
  const [breaker, outbox, invisibility] = await Promise.all([
    read<ProviderBreakerStatus>(() => api.getProviderBreaker()),
    read<OutboxEvent[]>(() => api.listOutboxEvents("slack")),
    manifestId ? read<InvisibilityReport | null>(() => api.getInvisibility(manifestId)) : Promise.resolve(null),
  ]);

  const v = systemView({ coreOk, breaker, outbox, invisibility, invisibilityAsked: manifestId !== null });

  return (
    <section className="page system">
      <header className="control-head">
        <h1>System</h1>
        <dl className="context-bar">
          <div>
            <dt>Core</dt>
            <dd>{mark(v.core.status)} {v.core.status === "ok" ? "ok" : "unreachable"}</dd>
          </div>
          <div>
            <dt>Provider breaker</dt>
            <dd>
              {mark(v.breaker.status)} {v.breaker.state}
            </dd>
          </div>
          <div>
            <dt>Slack outbox</dt>
            <dd>
              {mark(v.queue.status)} {v.queue.events === null ? "unreadable" : `${v.queue.events.length} unacked`}
            </dd>
          </div>
          <div>
            <dt>Held-out event</dt>
            <dd>
              {mark(v.invisibility.status)} {v.invisibility.report?.status ?? (v.invisibility.asked ? "not observed" : "not checked")}
            </dd>
          </div>
        </dl>
      </header>

      <div className="control-grid">
        <section className="card" aria-label="Provider breaker">
          <h2>Provider breaker</h2>
          <p className="sys-summary">
            <span className={`sys-mark ${v.breaker.status}`}>{mark(v.breaker.status)}</span> {v.breaker.summary}
          </p>
          {v.breaker.detail ? (
            <dl className="kv">
              <dt>Consecutive failures</dt>
              <dd>
                {v.breaker.detail.consecutive} / threshold {v.breaker.detail.threshold}
              </dd>
              <dt>Trips since start</dt>
              <dd>{v.breaker.detail.trips}</dd>
              <dt>Calls refused while open</dt>
              <dd>{v.breaker.detail.rejected}</dd>
              <dt>Opened at</dt>
              <dd>{v.breaker.detail.openedAt ? formatUtc(v.breaker.detail.openedAt) : "—"}</dd>
              <dt>Last reason</dt>
              <dd>{v.breaker.detail.lastReason || "—"}</dd>
            </dl>
          ) : (
            <p className="empty">The core did not answer <code>/provider-breaker</code>.</p>
          )}
        </section>

        <section className="card" aria-label="Surface outbox">
          <h2>Surface outbox · consumer <code>slack</code></h2>
          <p className="sys-summary">
            <span className={`sys-mark ${v.queue.status}`}>{mark(v.queue.status)}</span> {v.queue.summary}
          </p>
          {v.queue.events === null ? (
            <p className="empty">The core did not answer <code>/outbox/events</code>.</p>
          ) : v.queue.events.length > 0 ? (
            <ScrollTable label="Outbox events"><table className="explorer-table">
              <thead>
                <tr>
                  <th>id</th>
                  <th>topic</th>
                  <th>account</th>
                  <th>queued</th>
                </tr>
              </thead>
              <tbody>
                {v.queue.events.map((e) => (
                  <tr key={e.id}>
                    <td>
                      <code>{e.id}</code>
                    </td>
                    <td>
                      <code>{e.topic}</code>
                    </td>
                    <td>
                      <code title={e.account_id}>{e.account_id.slice(0, 8)}</code>
                    </td>
                    <td>{formatUtc(e.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table></ScrollTable>
          ) : null}
        </section>
      </div>

      <section className="card" aria-label="Held-out invisibility">
        <h2>Held-out event leak check</h2>
        <p className="sys-summary">
          <span className={`sys-mark ${v.invisibility.status}`}>{mark(v.invisibility.status)}</span> {v.invisibility.summary}
        </p>
        {v.invisibility.asked && v.invisibility.report ? (
          <>
            <dl className="kv">
              <dt>Held-out event</dt>
              <dd>
                <code>{v.invisibility.report.held_out_event_id}</code>
              </dd>
              <dt>Stores inspected</dt>
              <dd>{v.invisibility.report.checked.length > 0 ? v.invisibility.report.checked.join(", ") : "none (already released)"}</dd>
            </dl>
            {v.invisibility.report.leaks.length > 0 ? (
              <ScrollTable label="Invisibility leaks"><table className="explorer-table">
                <thead>
                  <tr>
                    <th>store</th>
                    <th>kind</th>
                    <th>id</th>
                  </tr>
                </thead>
                <tbody>
                  {v.invisibility.report.leaks.map((l, i) => (
                    <tr key={i}>
                      <td>
                        <code>{l.store}</code>
                      </td>
                      <td>{l.kind}</td>
                      <td>
                        <code>{l.id}</code>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table></ScrollTable>
            ) : null}
          </>
        ) : !v.invisibility.asked ? (
          <ControlManifestForm target="/system" submitLabel="Check leak" initial="" />
        ) : null}
      </section>
    </section>
  );
}
