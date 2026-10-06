import { expect, test } from "@playwright/test";

// Bucket 2's stored gate results reach the product: the episode page and the /evals overview render the rows core
// serves (the fixture core holds three for the Acme episode), and a gate with nothing stored reads "Not measured".

const ACME_EPISODE = "0e9e0000-0000-4000-8000-000000000a01";

test("the episode page shows the stored decision and action checks, and Not measured for the rest", async ({ page }) => {
  await page.goto(`/episodes/${ACME_EPISODE}`);
  const section = page.getByRole("region", { name: "Decision and action checks" });
  await expect(section).toBeVisible();
  const d4 = section.locator("#gate-D4");
  await expect(d4).toContainText("the person chose option B over the preferred option A");
  await expect(d4.locator(".verdict")).toHaveText("Pass");
  await expect(d4).toContainText("Not yet calibrated");
  await expect(section.locator("#gate-D2 .verdict")).toHaveText("Warn");
  await expect(section.locator("#gate-D1")).toContainText("Not measured");
  await expect(section.locator("#gate-D9")).toContainText("Not measured");
});

test("the evals overview renders the same stored rows for the episode it is pointed at", async ({ page }) => {
  await page.goto(`/evals?episode=${ACME_EPISODE}`);
  const d6 = page.locator("#gate-D6");
  await expect(d6).toContainText("no stored eval covers the cta dimension");
  await expect(d6.locator(".verdict")).toHaveText("Warn");
  await expect(page.locator("#gate-D4 .verdict")).toHaveText("Pass");
});
