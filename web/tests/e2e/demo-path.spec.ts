import { expect, test } from "@playwright/test";

// The demo path end to end: /control -> Play -> /episodes/:id -> /runs/:id/evals -> /system, with no console
// errors or uncaught page errors on any of them.
const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const CORE = `http://127.0.0.1:${process.env.E2E_CORE_PORT ?? 18080}`;
const TOKEN = "e2e-token";
const EPISODE = "0e9ead00-0000-4000-8000-00000000ad0a";
const RUN = "0f0aad00-0000-4000-8000-00000000ad60";

test("the demo path runs without console errors", async ({ page, request }) => {
  const problems: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error") problems.push(`console: ${m.text()}`);
  });
  page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));

  const reset = await request.post(`${CORE}/replay/manifests/${MANIFEST}/reset`, { headers: { authorization: `Bearer ${TOKEN}` }, data: { episode: 2 } });
  expect(reset.ok()).toBe(true);

  await page.goto(`/control?manifest=${MANIFEST}`);
  await page.getByRole("button", { name: "Play event N" }).click();
  const strip = page.getByRole("list", { name: "Pipeline stages" });
  await expect(strip.locator(".stage").nth(5)).toContainText("posted", { timeout: 15000 });

  // The fixture core's decision episode and run for the MedTech account (the played Acme episode has no fixture run).
  await expect(page.getByRole("link", { name: "episode" }).first()).toBeVisible();
  await page.goto(`/episodes/${EPISODE}?manifest=${MANIFEST}&node=cliff`);
  await expect(page.getByRole("list", { name: "Causal trajectory" })).toBeVisible();
  await page.goto(`/runs/${RUN}/evals`);
  await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();

  await page.goto("/system");
  await expect(page.getByRole("heading", { name: "System" })).toBeVisible();

  expect(problems).toEqual([]);
});
