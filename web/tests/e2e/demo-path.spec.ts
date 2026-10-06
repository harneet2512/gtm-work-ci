import { expect, test } from "@playwright/test";

// The demo path end to end, on the configured demo account: /control -> Play (live progress) -> the episode it opened ->
// /runs/:id/evals -> /evals results -> /system, with no console errors or uncaught page errors on any of them. The run
// comparison is operator-only: it is never reachable from the walkthrough and Demo mode does not list it.
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

  await page.goto("/control");
  await expect(page.getByRole("heading", { level: 1, name: "Acme Corp" })).toBeVisible();
  await page.getByRole("button", { name: "Play event N" }).click();
  await expect(page.locator(".pipeline-overall")).toHaveText("Pipeline complete", { timeout: 20000 });
  const strip = page.getByRole("list", { name: "Pipeline stages" });
  await expect(strip.locator(".stage").nth(6)).toContainText("completed");

  // The episode the played event opened, then the MedTech decision episode the fixture core holds a full chain for.
  await expect(page.getByRole("link", { name: "episode" }).first()).toBeVisible();
  await page.goto(`/episodes/${EPISODE}?node=${encodeURIComponent("cliff_message:chooser")}`);
  await expect(page.getByRole("list", { name: "Causal trajectory" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Knowledge mutations" })).toBeVisible();
  await page.goto(`/runs/${RUN}/evals`);
  await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
  await page.goto("/evals?view=results");
  await expect(page.getByRole("heading", { level: 1, name: "Results" })).toBeVisible();

  await page.goto("/system");
  await expect(page.getByRole("heading", { name: "System", exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Operational metrics" })).toBeVisible();

  expect(problems).toEqual([]);
});

test("Demo mode never offers the operator's run comparison", async ({ page }) => {
  await page.goto("/evals?view=results&demo=1");
  const tabs = page.getByRole("navigation", { name: "Eval views" });
  await expect(tabs).toContainText("Results");
  await expect(tabs).not.toContainText("comparison");
  // Even by URL, Demo mode shows Results rather than the comparison.
  await page.goto("/evals?view=compare&demo=1");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Results");
  await expect(page.getByText("Run comparison")).toHaveCount(0);
});
