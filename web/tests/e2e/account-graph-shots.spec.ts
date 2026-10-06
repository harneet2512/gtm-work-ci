import { expect, test, type Page } from "@playwright/test";
import path from "node:path";

// Product screenshots of the account graph on the MedTech case: Before Play, After Play and zoomed in with
// a node selected. Only runs when GRAPH_SHOTS_DIR names the folder to write them to.
const DIR = process.env.GRAPH_SHOTS_DIR ?? "";
const ACCOUNT = "0a0cad00-0000-4000-8000-00000000ad01";
const EVENT = "05e0ad00-0000-4000-8000-00000000ad0d";
const EVENT_ACTIVITY = "0ac7ad00-0000-4000-8000-00000000ad0d";
const THEME = process.env.GRAPH_SHOTS_THEME ?? "light";

test.skip(DIR === "", "set GRAPH_SHOTS_DIR to take the screenshots");
test.use({ viewport: { width: 1600, height: 1000 } });

async function shot(page: Page, name: string, whole = false): Promise<void> {
  const file = path.join(DIR, `${name}${THEME === "dark" ? "-dark" : ""}.png`);
  if (whole) return void (await page.screenshot({ path: file }));
  const clip = (await page.locator(".map-panel").boundingBox())!;
  await page.screenshot({ path: file, clip });
}

test("MedTech account graph, Before -> After -> zoomed in with a node selected", async ({ page }) => {
  await page.goto(`/accounts/${ACCOUNT}?event=${EVENT}`);
  if (THEME === "dark") await page.evaluate(() => document.documentElement.setAttribute("data-theme", "dark"));
  const stage = page.locator(".gx-stage");
  await expect(stage).toHaveAttribute("data-settled", "true", { timeout: 15_000 });
  await page.locator(".map-panel").scrollIntoViewIfNeeded();
  await page.waitForTimeout(400);
  await shot(page, "1-before-play");

  await page.getByRole("link", { name: "After Play" }).click();
  if (THEME === "dark") await page.evaluate(() => document.documentElement.setAttribute("data-theme", "dark"));
  await expect(stage).toHaveAttribute("data-entrance", "running", { timeout: 15_000 });
  await page.waitForTimeout(1100);
  await shot(page, "2-after-play-entrance");
  await expect(stage).toHaveAttribute("data-entrance", "done", { timeout: 15_000 });
  await expect(stage).toHaveAttribute("data-settled", "true", { timeout: 15_000 });
  await page.waitForTimeout(300);
  await shot(page, "3-after-play");

  // Click the event's own email (the camera flies to it), then zoom in once more: its edges are labelled.
  const twin = page.locator(`[data-node-id="${EVENT_ACTIVITY}"]`);
  const box = (await page.locator("canvas.gx-canvas").boundingBox())!;
  await page.mouse.click(box.x + Number(await twin.getAttribute("data-sx")), box.y + Number(await twin.getAttribute("data-sy")));
  await expect(stage).toHaveAttribute("data-focus", EVENT_ACTIVITY);
  await page.waitForTimeout(900);
  await page.getByRole("button", { name: "Zoom in" }).click();
  await page.waitForTimeout(900);
  await page.mouse.move(box.x + 5, box.y + box.height - 5);
  await page.waitForTimeout(300);
  await shot(page, "4-zoomed-selected");

  await page.getByRole("button", { name: "Expand the graph" }).click();
  await page.waitForTimeout(900);
  await shot(page, "5-full-screen", true);
});
