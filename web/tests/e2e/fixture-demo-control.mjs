// A stand-in for the demo's loopback control service (core-go internal/codespace Control): GET /status and the hidden
// POST /handoff the Play action calls when the account on screen has no episode left. The handoff takes a while, like the
// real one (carry, graph rebuild, core restart), and the fixture core fails every replay read meanwhile. Fixture-only
// routes (/__arm, /__release_all, /__reset) let a test set the scene; no other test sees a second case unless it arms one.
import { createServer } from "node:http";
import { beginHandoff, demoManifestId, finishHandoff, releaseEverything, resetHandoff } from "./fixture-replay.mjs";

export const NEXT_MANIFEST = "0d3a0000-0000-4000-8000-000000000502";
const ACCOUNT = "0d3a0000-0000-4000-8000-000000000102";
const HANDOFF_MS = Number(process.env.FIXTURE_HANDOFF_MS ?? 2500);

export function startDemoControl({ port, token }) {
  let armed = false;
  const json = (res, status, body) => {
    res.writeHead(status, { "content-type": "application/json" });
    res.end(JSON.stringify(body));
  };
  const status = () => ({
    ready: true,
    phase: "ready",
    message: "ready",
    services: [],
    cases: armed
      ? [
          { slot: "case1", label: "Acme", seeded: true, active: false, manifest_id: demoManifestId() },
          { slot: "case2", label: "Next account", seeded: true, active: false, manifest_id: NEXT_MANIFEST, account_id: ACCOUNT },
        ]
      : [],
    missing_secrets: [],
    checked_at: "2026-10-05T10:00:00Z",
  });
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? "/", `http://${req.headers.host}`);
    if (url.pathname === "/healthz") return json(res, 200, { status: "ok" });
    if (url.pathname === "/__arm") {
      armed = true;
      return json(res, 200, {});
    }
    if (url.pathname === "/__release_all") {
      releaseEverything();
      return json(res, 200, {});
    }
    if (url.pathname === "/__reset") {
      armed = false;
      resetHandoff();
      return json(res, 200, {});
    }
    if (req.headers.authorization !== `Bearer ${token}`) return json(res, 401, { error: { code: "unauthorized", message: "token required" } });
    if (req.method === "GET" && url.pathname === "/status") return json(res, 200, status());
    if (req.method === "POST" && url.pathname === "/handoff") {
      beginHandoff();
      return void setTimeout(() => {
        finishHandoff(NEXT_MANIFEST);
        json(res, 200, { slot: "case2", label: "Next account", manifest_id: NEXT_MANIFEST, account_id: ACCOUNT });
      }, HANDOFF_MS);
    }
    return json(res, 404, { error: { code: "not_found", message: "no such endpoint" } });
  });
  server.listen(port, "127.0.0.1", () => console.log(`fixture demo control on http://127.0.0.1:${port}`));
  return server;
}
