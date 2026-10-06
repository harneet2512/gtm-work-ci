import { expect, test, type APIRequestContext } from "@playwright/test";

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const CORE = `http://127.0.0.1:${process.env.E2E_CORE_PORT ?? 18080}`;
const TOKEN = "e2e-token";

/** The fixture core is shared and stateful: each test first puts the replay cursor where it needs it. */
async function releaseTo(request: APIRequestContext, episode: number) {
  const res = await request.post(`${CORE}/replay/manifests/${MANIFEST}/reset`, { headers: { authorization: `Bearer ${TOKEN}` }, data: { episode } });
  if (!res.ok()) throw new Error(`fixture reset to ${episode} failed: ${res.status()}`);
}

test("system shows the honest health reads, opened on the configured demo account", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto("/system");
  await expect(page.getByRole("heading", { name: "System", exact: true })).toBeVisible();

  // Context bar: backend healthy, breaker closed, delivery queue drained, the held-out event checked on the demo account.
  const bar = page.locator(".context-bar");
  await expect(bar).toContainText("Provider breaker");
  await expect(bar).toContainText("closed");
  await expect(bar).toContainText("0 waiting");
  await expect(bar).toContainText("withheld");

  // No check mark: a reading being fine is not an eval verdict.
  await expect(bar).not.toContainText("✓");
  await expect(page.getByText(/Closed — no provider failures counted/)).toBeVisible();
  await expect(page.getByText("Drained: nothing is waiting for delivery")).toBeVisible();
  await expect(page.getByText(/Withheld — 2 stores inspected, nothing leaked/)).toBeVisible();
  await expect(page.getByText("records, graph")).toBeVisible();
});

test("a run with no recorded model usage reads not measured, never zero and never free", async ({ page, request }) => {
  await releaseTo(request, 3); // the latest decision episode is the one Play event 3 opens: no usage was recorded for it
  await page.goto("/system");
  const panel = page.getByRole("region", { name: "Operational metrics" });
  await expect(panel).toContainText("Metrics describe cost and speed. They have no verdict.");
  await expect(panel).toContainText("No model usage was recorded for this run");
  for (const label of ["Cost", "Input tokens", "Cached input tokens"]) await expect(panel.locator(".kv-row", { hasText: label }).first()).toContainText("not measured");
  await expect(panel).not.toContainText(/\$\d/);
  await expect(panel.getByRole("table")).toHaveCount(0);
});

test("an episode the backend has no metrics for is said, not shown as zero", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto("/system");
  await expect(page.getByRole("region", { name: "Operational metrics" })).toContainText("No metrics are recorded for that episode.");
});

test("system names no developer tooling, endpoint or ticket", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto("/system");
  const text = (await page.locator("main").innerText()).toLowerCase();
  for (const banned of ["/healthz", "/outbox", "/provider-breaker", "har-", "postgres", "neo4j", "slack outbox"]) expect(text).not.toContain(banned);
});

test("system is on the rail under System", async ({ page }) => {
  await page.goto("/system");
  const rail = page.getByRole("navigation", { name: "Sections" });
  await expect(rail.locator(".rail-label", { hasText: /^System$/ })).toBeVisible();
  const link = rail.getByRole("link", { name: "System" });
  await expect(link).toHaveAttribute("aria-current", "page");
});
