import { expect, test, type APIRequestContext } from "@playwright/test";

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const PAGE = `/control?manifest=${MANIFEST}`;
const CORE = `http://127.0.0.1:${process.env.E2E_CORE_PORT ?? 18080}`; // the fixture core playwright.config.ts starts
const TOKEN = "e2e-token";

/** The fixture core is shared and stateful: a mutating test first returns the cursor to a known position. */
async function releaseTo(request: APIRequestContext, episode: number) {
  const res = await request.post(`${CORE}/replay/manifests/${MANIFEST}/reset`, {
    headers: { authorization: `Bearer ${TOKEN}` },
    data: { episode },
  });
  if (!res.ok()) throw new Error(`fixture reset to ${episode} failed: ${res.status()}`);
}

const stage = (page: import("@playwright/test").Page, n: number) => page.getByRole("list", { name: "Pipeline stages" }).locator(".stage").nth(n);

test("control opens on the configured demo account without anyone typing an id", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto("/control");
  await expect(page.getByRole("heading", { level: 1, name: "Acme Corp" })).toBeVisible();
  await expect(page.getByLabel("Demo manifest id")).toHaveCount(0);
});

test("an invalid account id is said, and the id form is the fallback", async ({ page }) => {
  await page.goto("/control?manifest=nope");
  await expect(page.getByText("That account id is not a valid id.")).toBeVisible();
  await expect(page.getByLabel("Demo manifest id")).toBeVisible();
});

test("the control page shows the world at the cursor, the held-out event and four honest health bands", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto(PAGE);

  await expect(page.getByRole("heading", { level: 1, name: "Acme Corp" })).toBeVisible();
  const bar = page.locator(".context-bar");
  await expect(bar).toContainText("2 / 4");
  await expect(bar).toContainText("historical");

  // The held-out event's public metadata is shown; its content stays withheld. Before Play nothing has run.
  await expect(page.getByRole("button", { name: "Play event N" })).toBeEnabled();
  await expect(page.locator(".pipeline-head")).toContainText("Held out");
  await expect(page.locator(".pipeline-strip .stage.st-waiting")).toHaveCount(7);
  await expect(page.locator(".pipeline-overall")).toHaveText("Not started");

  const bands = page.getByRole("region", { name: "Area health" }).locator("article");
  await expect(bands).toHaveCount(4);
  // Real counts from the account's latest eval run; areas with no results read "not measured", never healthy.
  await expect(bands.nth(0)).toContainText("Intelligence");
  await expect(bands.nth(0)).toContainText("not measured");
  await expect(bands.nth(1)).toContainText("3 pass · 2 warn · 2 fail · 0 unknown");
  await expect(bands.nth(1)).toContainText("vs previous episode");
  // Action generation (E12) is part of Decision & Learning now, so Cliff / Experience holds no eval family.
  await expect(bands.nth(2)).toContainText("not measured");
  await expect(bands.nth(3)).toContainText("not measured");

  const changes = page.getByRole("region", { name: "Recent material changes" });
  await expect(changes).toContainText("E1");

  const rail = page.getByRole("navigation", { name: "Episode trajectory" });
  await expect(rail.getByRole("listitem")).toHaveCount(3); // E1, E2 released + E3 held out
});

test("Play event N follows the real pipeline: stages commit one read at a time, finished stages read completed, never PASS", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto(PAGE);
  await page.getByRole("button", { name: "Play event N" }).click();

  // While the Play request is open the strip polls progress: some stage is running before the pipeline is complete.
  await expect(page.locator(".pipeline-strip .stage.st-running").first()).toBeVisible({ timeout: 10000 });
  await expect(page.locator(".pipeline-overall")).toHaveText("Running");

  await expect(page.locator(".pipeline-overall")).toHaveText("Pipeline complete", { timeout: 20000 });
  for (const n of [0, 1, 2, 3, 4, 6]) {
    await expect(stage(page, n)).toContainText("completed");
    await expect(stage(page, n)).not.toContainText("passed");
    await expect(stage(page, n).locator(".stage-mark")).not.toHaveText("✓");
  }
  // Only the evals stage carries a verdict, from the played run's real results (which include warns and fails).
  await expect(stage(page, 5)).toContainText("warning");
  await expect(page.locator(".pipeline-strip .stage-mark", { hasText: "✓" })).toHaveCount(0);
  await expect(page.locator(".pipeline-line")).toContainText("was material");
});

test("a non-material event is honest: the downstream stages are skipped, not green", async ({ page, request }) => {
  // Episode 2 is the manifest's non-material event: releasing just it skips everything after resolve.
  await releaseTo(request, 1);
  await page.goto(PAGE);
  await page.getByRole("button", { name: "Play event N" }).click();

  await expect(page.locator(".pipeline-overall")).toHaveText("Pipeline complete", { timeout: 20000 });
  await expect(stage(page, 0)).toContainText("completed");
  await expect(stage(page, 1)).toContainText("completed");
  for (const n of [2, 3, 4, 5, 6]) await expect(stage(page, n)).toContainText("skipped");
  await expect(page.locator(".pipeline-line")).toContainText("no action required");
});

test("a reload after Play shows what the pipeline already did", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto(PAGE);
  await page.getByRole("button", { name: "Play event N" }).click();
  await expect(page.locator(".pipeline-overall")).toHaveText("Pipeline complete", { timeout: 20000 });
  await page.reload();
  await expect(page.locator(".pipeline-overall")).toHaveText("Pipeline complete");
  await expect(stage(page, 5)).toContainText("warning");
});

test("the history link opens the Before Play timeline without firing any POST, and keeps demo mode", async ({ page, request }) => {
  await releaseTo(request, 2);
  const posts: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST") posts.push(r.url());
  });
  await page.goto(`${PAGE}&demo=1`);
  const link = page.getByRole("link", { name: "View history (2 events before Event 3)" });
  await expect(link).toHaveAttribute("href", /\/accounts\/0a0c0000-0000-4000-8000-000000000001\?view=before&demo=1$/);
  await link.click();

  await expect(page).toHaveURL(/\/accounts\/0a0c0000-0000-4000-8000-000000000001\?view=before&demo=1$/);
  await expect(page.getByRole("heading", { name: "Before Play: account map" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Timeline" })).toBeVisible();
  expect(posts).toEqual([]);
});
