import { firstParam } from "@/lib/params";
import { core } from "@/lib/api/server";
import { manifestNotice, resolveManifest } from "@/lib/demo-manifest";
import { loadSystemPage } from "@/lib/load-system";
import { storeLabel, systemView, type BandStatus } from "@/lib/view/system";
import { formatUtc } from "@/lib/format";
import { ControlManifestForm } from "@/components/control/ControlManifestForm";
import { Metrics } from "@/components/system/Metrics";
import { ScrollTable } from "@/components/ScrollTable";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;

// A neutral glyph per state: the check mark belongs to eval verdicts, never to "this reading is fine".
const mark = (s: BandStatus): string => (s === "ok" ? "●" : s === "warn" ? "!" : s === "bad" ? "×" : "?");

/**
 * /system: system diagnostics (HAR-145). Is the backend up, is the model provider breaker holding calls, is the Cliff
 * delivery queue draining, did the held-out event stay invisible, and what did the latest decision cost (metrics). Every
 * row is a real read; a failed read shows as unknown or unavailable, never as fine. It opens on the configured demo account.
 */
export default async function SystemPage({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  const { manifestId, source } = resolveManifest(firstParam(sp.manifest), process.env);
  const data = await loadSystemPage(core(), manifestId);
  const v = systemView({ coreOk: data.coreOk, breaker: data.breaker, outbox: data.outbox, invisibility: data.invisibility, invisibilityAsked: data.invisibilityAsked });

  return (
    <section className="page system">
      <header className="control-head">
        <h1>System</h1>
        <dl className="context-bar">
          <div>
            <dt>Backend</dt>
            <dd>
              {mark(v.core.status)} {v.core.status === "ok" ? "healthy" : "unreachable"}
            </dd>
          </div>
          <div>
            <dt>Provider breaker</dt>
            <dd>
              {mark(v.breaker.status)} {v.breaker.state}
            </dd>
          </div>
          <div>
            <dt>Cliff delivery queue</dt>
            <dd>
              {mark(v.queue.status)} {v.queue.events === null ? "unreadable" : `${v.queue.events.length} waiting`}
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
            <p className="empty">Backend unavailable: the provider breaker could not be read.</p>
          )}
        </section>

        <section className="card" aria-label="Cliff delivery queue">
          <h2>Cliff delivery queue</h2>
          <p className="sys-summary">
            <span className={`sys-mark ${v.queue.status}`}>{mark(v.queue.status)}</span> {v.queue.summary}
          </p>
          {v.queue.events === null ? (
            <p className="empty">Backend unavailable: the delivery queue could not be read.</p>
          ) : v.queue.events.length > 0 ? (
            <ScrollTable label="Delivery queue events">
              <table className="explorer-table">
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
              </table>
            </ScrollTable>
          ) : null}
        </section>
      </div>

      <Metrics state={data.metrics} />

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
              <dd>{v.invisibility.report.checked.length > 0 ? v.invisibility.report.checked.map(storeLabel).join(", ") : "none (already released)"}</dd>
            </dl>
            {v.invisibility.report.leaks.length > 0 ? (
              <ScrollTable label="Invisibility leaks">
                <table className="explorer-table">
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
                          <code>{storeLabel(l.store)}</code>
                        </td>
                        <td>{l.kind}</td>
                        <td>
                          <code>{l.id}</code>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </ScrollTable>
            ) : null}
          </>
        ) : !v.invisibility.asked ? (
          <>
            {manifestNotice(source) ? <p className="notices" role="status">{manifestNotice(source)}</p> : null}
            <ControlManifestForm target="/system" submitLabel="Check leak" initial="" />
          </>
        ) : null}
      </section>
    </section>
  );
}
