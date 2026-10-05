// The Play strip's poll target (HAR-145). A browser cannot hold the core token, and a server action cannot run beside
// the open Play request, so the strip polls this same-origin GET, which reads the manifest's progress server-side.
import { CoreError, InvalidIdError, errorCode } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";

export const dynamic = "force-dynamic";

const NO_STORE = { "Cache-Control": "no-store" };
const fail = (status: number, code: string) => Response.json({ error: { code } }, { status, headers: NO_STORE });

export async function GET(_request: Request, { params }: { params: Promise<{ manifestId: string }> }): Promise<Response> {
  const { manifestId } = await params;
  try {
    const progress = await core().getReplayProgress(manifestId);
    return progress ? Response.json(progress, { headers: NO_STORE }) : fail(404, "manifest_not_found");
  } catch (e) {
    if (e instanceof InvalidIdError) return fail(400, "bad_request");
    // Any other failure means the core could not answer: the strip reads "backend unavailable", never a pipeline failure.
    return fail(502, e instanceof CoreError ? errorCode(e, "backend_unavailable") : "backend_unavailable");
  }
}
