import { notFound } from "next/navigation";
import { AskTraceView } from "@/components/ask/AskTraceView";
import { CoreError, InvalidIdError } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";

export const dynamic = "force-dynamic";

type Params = Promise<{ traceId: string }>;

/** How one Cliff answer was made (GET /ask/traces/{trace_id}): the tools it called, what they returned, tokens and cost. */
export default async function AskTracePage({ params }: { params: Params }) {
  const { traceId } = await params;
  let trace;
  try {
    trace = await core().getAskTrace(traceId);
  } catch (e) {
    if (e instanceof InvalidIdError || (e instanceof CoreError && e.status === 404)) notFound();
    throw e;
  }
  return (
    <section className="page">
      <div className="head">
        <h1>How Cliff answered</h1>
      </div>
      <AskTraceView trace={trace} />
    </section>
  );
}
