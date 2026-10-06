import Link from "next/link";
import type { EvalRunsData, FamiliesData } from "@/lib/load-eval-runs";
import { areaViews, evalRunRow, type AreaView } from "@/lib/view/eval-runs";
import { TONE_MARK, TONE_WORD } from "@/lib/view/bands";
import { formatUtc } from "@/lib/format";
import { ScrollTable } from "@/components/ScrollTable";

const RESULTS = "/evals?view=results";

/** The four areas of one run, each with its real counts, its delta against the previous episode, and its families. */
function AreaSection({ area }: { area: AreaView }) {
  return (
    <section className={`area-block tone-${area.tone}`} aria-label={`${area.label} results`}>
      <header>
        <span className="band-mark" aria-hidden="true">
          {TONE_MARK[area.tone]}
        </span>
        <h3>{area.label}</h3>
        <span className="sr-only">{TONE_WORD[area.tone]}</span>
        <span className="area-status">{area.status}</span>
      </header>
      {area.counts ? (
        <p className="hint">
          {area.counts}
          {area.blocking ? ` · ${area.blocking}` : ""}
          {area.delta ? ` · ${area.delta}` : ""}
        </p>
      ) : (
        <p className="hint">No result of this area was recorded for this run, so it is not measured. It is not a pass.</p>
      )}
      {area.families.map((f) => (
        <details key={f.id} className="family" open>
          <summary>
            <span className="family-name">{f.name}</span>
            <span className="family-counts">{f.counts}</span>
            <span className="hint">{f.delta}</span>
          </summary>
          <ScrollTable label={`${f.name} eval types`}>
            <table className="dense">
              <thead>
                <tr>
                  <th>Eval</th>
                  <th>Counts</th>
                  <th>Results</th>
                  <th>Change</th>
                </tr>
              </thead>
              <tbody>
                {f.evalTypes.map((t) => (
                  <tr key={t.evalType}>
                    <td>{t.name}</td>
                    <td>{t.counts}</td>
                    <td>{t.results}</td>
                    <td className="hint">{t.delta}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </ScrollTable>
        </details>
      ))}
    </section>
  );
}

/**
 * The evals explorer (HAR-145): the eval runs, newest first, each with its four areas' real counts; selecting a run opens
 * its areas, families and eval types. Every figure is a tally of recorded results: no percentage, no score. Deltas
 * compare with the account's previous episode, which is a different event.
 */
export function EvalRuns({ data, selectedId, families }: { data: EvalRunsData; selectedId: string | null; families: FamiliesData | null }) {
  const rows = data.runs.map(evalRunRow);
  return (
    <div className="eval-runs">
      {data.unavailable ? (
        <p className="notices" role="status">
          Backend unavailable: the eval runs could not be read. This is not an eval failure.
        </p>
      ) : null}
      {data.badCursor ? (
        <p className="notices" role="status">
          That page marker was not valid, so the newest runs are shown.
        </p>
      ) : null}
      {!data.unavailable && rows.length === 0 ? <p className="hint">No eval run has been recorded yet.</p> : null}
      {rows.length > 0 ? (
        <ScrollTable label="Eval runs">
          <table className="dense explorer-table">
            <thead>
              <tr>
                <th>Run</th>
                <th>Account</th>
                <th>Results</th>
                <th>Intelligence</th>
                <th>Decision &amp; Learning</th>
                <th>Cliff / Experience</th>
                <th>System</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.id} className={r.id === selectedId ? "selected" : undefined} aria-selected={r.id === selectedId}>
                  <td>
                    <Link href={`${RESULTS}&run=${r.id}`} aria-current={r.id === selectedId ? "true" : undefined}>
                      {formatUtc(r.evaluatedAt)}
                    </Link>
                    <span className="etype mono">{r.id.slice(0, 8)}</span>
                  </td>
                  <td>{r.account}</td>
                  <td>{r.total}</td>
                  {r.areas.map((a) => (
                    <td key={a.id} className={`area-chip tone-${a.tone}`}>
                      <span aria-hidden="true">{TONE_MARK[a.tone]} </span>
                      <span className="sr-only">{TONE_WORD[a.tone]}: </span>
                      {a.text}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </ScrollTable>
      ) : null}
      {data.nextCursor ? (
        <p>
          <Link href={`${RESULTS}&cursor=${encodeURIComponent(data.nextCursor)}`}>Older runs</Link>
        </p>
      ) : null}

      {selectedId ? (
        <section className="panel" aria-label="Selected eval run">
          <h2>Run {selectedId.slice(0, 8)}</h2>
          <p className="hint">
            <Link href={`/runs/${selectedId}/evals`}>This run&apos;s evals with their reasons</Link>
            {rows.find((r) => r.id === selectedId)?.episodeId ? (
              <>
                {" · "}
                <Link href={`/episodes/${rows.find((r) => r.id === selectedId)!.episodeId}`}>Its episode</Link>
              </>
            ) : null}
          </p>
          {families?.unavailable ? (
            <p className="notices" role="status">
              Backend unavailable: this run&apos;s families could not be read. This is not an eval failure.
            </p>
          ) : families?.summary ? (
            areaViews(families.summary).map((a) => <AreaSection key={a.id} area={a} />)
          ) : (
            <p className="hint">That run has no recorded eval results.</p>
          )}
        </section>
      ) : rows.length > 0 ? (
        <p className="hint">Select a run to see its areas, families and eval types.</p>
      ) : null}
    </div>
  );
}
