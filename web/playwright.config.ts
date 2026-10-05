import { defineConfig, devices } from "@playwright/test";

// Overridable so parallel worktrees can run e2e side by side without reusing each other's servers.
const CORE_PORT = Number(process.env.E2E_CORE_PORT ?? 18080);
const WEB_PORT = Number(process.env.E2E_WEB_PORT ?? 3100);
const TOKEN = "e2e-token";

// Two servers, no live core: a replay of recorded core responses, and the built web app pointed at it.
export default defineConfig({
  testDir: "tests/e2e",
  testMatch: "*.spec.ts",
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: { baseURL: `http://127.0.0.1:${WEB_PORT}`, trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: [
    {
      command: "node tests/e2e/fixture-core.mjs",
      url: `http://127.0.0.1:${CORE_PORT}/healthz`,
      env: { FIXTURE_CORE_PORT: String(CORE_PORT), FIXTURE_CORE_TOKEN: TOKEN },
      reuseExistingServer: !process.env.CI,
    },
    {
      command: `npm run build && npx next start -H 127.0.0.1 -p ${WEB_PORT}`,
      url: `http://127.0.0.1:${WEB_PORT}`,
      env: { CORE_URL: `http://127.0.0.1:${CORE_PORT}`, GHOST_API_TOKEN: TOKEN },
      reuseExistingServer: !process.env.CI,
      timeout: 240_000,
    },
  ],
});
