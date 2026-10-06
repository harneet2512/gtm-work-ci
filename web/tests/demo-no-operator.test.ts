// Product-owner rule for the demo: the audience sees the Web Control Plane and Cliff in Slack, and Play is the only visible
// trigger. There is no operator control, admin page or plumbing in the audience app: no Reset or Continue button, no
// service list, no model-call counts, no status endpoint. Start, Stop and Reset are desktop shortcuts outside the app.
// These checks read the source tree, so a reintroduced control fails here.
import { existsSync, readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const WEB = path.resolve(__dirname, "..");
const read = (rel: string) => readFileSync(path.join(WEB, rel), "utf8");
const code = (rel: string) =>
  read(rel)
    .split("\n")
    .filter((l) => !/^\s*(\/\/|\*|\/\*)/.test(l))
    .join("\n");

function sources(dir: string): string[] {
  return readdirSync(path.join(WEB, dir), { withFileTypes: true }).flatMap((e) => {
    const rel = `${dir}/${e.name}`;
    if (e.isDirectory()) return sources(rel);
    return /\.(ts|tsx)$/.test(e.name) ? [rel] : [];
  });
}

const APP = [...sources("app"), ...sources("components"), ...sources("lib")];

describe("no operator controls in the audience app", () => {
  it("has no admin route, operator component or status endpoint", () => {
    for (const gone of ["app/admin", "components/demo", "app/api/status", "app/system/actions.ts", "lib/demo/use-demo-status.ts"]) {
      expect(existsSync(path.join(WEB, gone)), gone).toBe(false);
    }
  });

  it("links to and redirects to /admin nowhere", () => {
    expect(APP.filter((f) => /["'`]\/admin\b/.test(code(f)))).toEqual([]);
  });

  it("has no Reset, Continue or switch-case control anywhere", () => {
    const offenders = APP.filter((f) => /DemoOperator|resetDemo|continueDemo|postReset|postCase|\/api\/status|use-demo-status|Continue with/.test(code(f)));
    expect(offenders).toEqual([]);
  });

  it("shows no model-call or readiness status", () => {
    expect(APP.filter((f) => /demo-llm|spend_blocked|Replaying recorded|Recording new call|All systems ready/.test(code(f)))).toEqual([]);
  });

  it("keeps the System page to platform health: no demo service section", () => {
    expect(code("app/system/page.tsx")).not.toMatch(/demo\/|fetchStatus|DemoOperator/);
  });

  it("does not let a forwarded cloud host call server actions", () => {
    expect(code("next.config.ts")).not.toMatch(/github\.dev|\*\./);
  });

  it("mentions no Reset or admin page in the app config (even in a comment)", () => {
    expect(read("next.config.ts")).not.toMatch(/reset|admin/i);
  });
});

describe("the shared header carries no demo status", () => {
  it("does not import demo code or poll a status", () => {
    expect(code("app/layout.tsx")).not.toMatch(/lib\/demo|components\/demo|DemoStatus|api\/status/);
  });
});

describe("the control service is used only server side, for the landing page and the hidden handoff", () => {
  it("is imported only by server code", () => {
    const users = APP.filter((f) => /lib\/demo\//.test(code(f)) && !f.startsWith("lib/demo/")).sort();
    expect(users).toEqual(["app/control/actions.ts", "app/control/page.tsx", "app/page.tsx"]);
    expect(read("app/control/actions.ts")).toMatch(/^["']use server["']/m);
  });
});
