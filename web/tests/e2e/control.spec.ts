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

test("control needs a manifest and opens one by id", async ({ page }) => {
  await page.goto("/control");
  await page.getByLabel("Demo manifest id").fill(MANIFEST);
  await page.getByRole("button", { name: "Open control" }).click();
  await expect(page).toHaveURL(new RegExp(`/control\\?manifest=${MANIFEST}$`));
});

test("the control page shows the world at the cursor, the held-out event and the health bands", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto(PAGE);

  await expect(page.getByRole("heading", { level: 1, name: "Acme Corp" })).toBeVisible();
  const bar = page.locator(".context-bar");
  await expect(bar).toContainText("2 / 4");
  await expect(bar).toContainText("historical");

  // The held-out event's public metadata is shown; its content stays withheld.
  await expect(page.getByRole("button", { name: "Play event N" })).toBeEnabled();
  await expect(page.locator(".pipeline-head")).toContainText("Held out");

  const bands = page.getByRole("region", { name: "Area health" }).locator("article");
  await expect(bands).toHaveCount(4);
  await expect(bands.first()).toContainText("Intelligence");

  const changes = page.getByRole("region", { name: "Recent material changes" });
  await expect(changes).toContainText("E1");

  const rail = page.getByRole("navigation", { name: "Episode trajectory" });
  await expect(rail.getByRole("listitem")).toHaveCount(3); // E1, E2 released + E3 held out
});

test("Play event N walks the real pipeline: ingest→state pass on release, decide+cliff follow the run and posted refs", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto(PAGE);

  await page.getByRole("button", { name: "Play event N" }).click();

  const strip = page.getByRole("list", { name: "Pipeline stages" });
  // The advance result's own artifacts light the first four stages on return.
  await expect(strip.locator(".stage").nth(0)).toContainText("passed");
  await expect(strip.locator(".stage").nth(2)).toContainText("passed");
  await expect(strip.locator(".stage").nth(3)).toContainText("state v8");
  // The episode's published run and posted refs settle decide + cliff — the strip polls real state.
  await expect(strip.locator(".stage").nth(4)).toContainText("passed", { timeout: 15000 });
  await expect(strip.locator(".stage").nth(5)).toContainText("posted", { timeout: 15000 });
});

test("a non-material event is honest: the downstream stages are skipped, not green", async ({ page, request }) => {
  // Episode 2 is the manifest's non-material event: releasing just it skips graph/state/decide/cliff.
  await releaseTo(request, 1);
  await page.goto(PAGE);
  await page.getByRole("button", { name: "Play event N" }).click();

  const strip = page.getByRole("list", { name: "Pipeline stages" });
  await expect(strip.locator(".stage").nth(0)).toContainText("passed");
  await expect(strip.locator(".stage").nth(3)).toContainText("skipped");
  await expect(strip.locator(".stage").nth(4)).toContainText("skipped");
  await expect(page.locator(".pipeline-line")).toContainText("no action required");
});
