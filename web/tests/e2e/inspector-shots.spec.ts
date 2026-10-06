import { expect, test } from "@playwright/test";
import path from "node:path";

// Product screenshots of the HAR-149 eval inspector. Only runs when HAR149_SHOTS_DIR names the folder to write them to.
const DIR = process.env.HAR149_SHOTS_DIR ?? "";
test.skip(DIR === "", "set HAR149_SHOTS_DIR to take the screenshots");
test.use({ viewport: { width: 1440, height: 1000 } });

const MEDTECH = "0e9ead00-0000-4000-8000-00000000ad0a";
const shot = (page: import("@playwright/test").Page, name: string, full = false) => page.screenshot({ path: path.join(DIR, `har149-${name}.png`), fullPage: full });

test("inspector screenshots (test data: the page says so)", async ({ page }) => {
  await page.goto("/evals/loop?demo=1");
  await expect(page.getByRole("heading", { name: "Do we understand what is happening?" })).toBeVisible();
  await shot(page, "1-landing-loop", true);

  await page.goto(`/evals/episode/${MEDTECH}?demo=1`);
  await expect(page.getByTestId("path-edit")).toBeVisible();
  await shot(page, "2-episode-path", true);

  await page.goto(`/evals/episode/${MEDTECH}?gate=B5&demo=1`);
  await expect(page.getByTestId("eval-drawer")).toBeVisible();
  await page.waitForTimeout(400);
  await shot(page, "3-drawer-b5");

  await page.goto(`/evals/episode/${MEDTECH}?gate=D3&demo=1`);
  await expect(page.getByTestId("eval-drawer")).toBeVisible();
  await page.waitForTimeout(400);
  await shot(page, "4-drawer-d3");
  await page.getByTestId("eval-drawer").locator(".dr-body").evaluate((el) => el.scrollTo(0, el.scrollHeight));
  await shot(page, "4b-drawer-d3-lower");

  await page.goto(`/evals/episode/${MEDTECH}/decision?demo=1`);
  await expect(page.getByTestId("candidates")).toBeVisible();
  await shot(page, "5-candidates", true);

  await page.goto("/evals/offline?demo=1");
  await expect(page.getByTestId("offline-headline")).toBeVisible();
  await shot(page, "6-offline", true);

  await page.goto("/evals/health?demo=1");
  await expect(page.getByRole("heading", { name: "Are the traces complete?" })).toBeVisible();
  await shot(page, "7-continuous-health", true);
});
