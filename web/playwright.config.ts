import { defineConfig, devices } from "@playwright/test";

// Overridable so parallel worktrees can run e2e side by side without reusing each other's servers.
const CORE_PORT = Number(process.env.E2E_CORE_PORT ?? 18080);
const CONTROL_TOKEN = "e2e-control-token"; // not the core token: the handoff is not callable with it
const CONTROL_PORT = Number(process.env.E2E_CONTROL_PORT ?? 18081);
const WEB_PORT = Number(process.env.E2E_WEB_PORT ?? 3100);
const TOKEN = "e2e-token";
const DEMO_MANIFEST = "0d3a0000-0000-4000-8000-000000000501";

// Two servers, no live core: a replay of recorded core responses, and the built web app pointed at it.
export default defineConfig({
  testDir: "tests/e2e",
  testMatch: "*.spec.ts",
  fullyParallel: false,
  // The fixture core is one stateful process (the replay cursor and the Play in flight): spec files must not interleave.
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: { baseURL: `http://127.0.0.1:${WEB_PORT}`, trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: [
    {
      command: "node tests/e2e/fixture-core.mjs",
      url: `http://127.0.0.1:${CORE_PORT}/healthz`,
      env: { FIXTURE_CORE_PORT: String(CORE_PORT), FIXTURE_CORE_TOKEN: TOKEN, FIXTURE_CONTROL_PORT: String(CONTROL_PORT), FIXTURE_CONTROL_TOKEN: CONTROL_TOKEN },
      reuseExistingServer: !process.env.CI,
    },
    {
      command: `npm run build && npx next start -H 127.0.0.1 -p ${WEB_PORT}`,
      url: `http://127.0.0.1:${WEB_PORT}`,
      // The demo manifest is configuration (the core serves no manifest list): /control, /replay and /system open on it.
      env: { GTM_TEST_DATA: "1", CORE_URL: `http://127.0.0.1:${CORE_PORT}`, GHOST_API_TOKEN: TOKEN, GHOST_DEMO_MANIFEST_ID: DEMO_MANIFEST, GHOST_DEMO_CONTROL_URL: `http://127.0.0.1:${CONTROL_PORT}`, GHOST_DEMO_CONTROL_TOKEN: CONTROL_TOKEN },
      reuseExistingServer: !process.env.CI,
      timeout: 240_000,
    },
  ],
});
