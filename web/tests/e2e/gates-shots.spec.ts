import { expect, test, type Page } from "@playwright/test";
import path from "node:path";

// Product screenshots of the evals table, the inspector open and the episode trace, in both themes. Only runs when
// GATES_SHOTS_DIR names the folder to write them to.
const DIR = process.env.GATES_SHOTS_DIR ?? "";
test.skip(DIR === "", "set GATES_SHOTS_DIR to take the screenshots");
test.use({ viewport: { width: 1600, height: 1000 } });

for (const theme of ["light", "dark"] as const) {
  const shot = (page: Page, name: string) => page.screenshot({ path: path.join(DIR, `${name}-${theme}.png`) });
  const setTheme = (page: Page) => page.evaluate((t) => document.documentElement.setAttribute("data-theme", t), theme);

  test(`evals table, inspector and episode trace (${theme})`, async ({ page }) => {
    await page.goto("/evals");
    await setTheme(page);
    await expect(page.getByRole("table")).toBeVisible();
    await shot(page, "1-evals-table");

    await page.locator("tbody tr[data-gate='B2'][data-measured='true']").click();
    await expect(page.getByRole("complementary", { name: "Inspector for B2" })).toBeVisible();
    await shot(page, "2-inspector-result");
    await page.getByRole("tab", { name: /Evidence/ }).click();
    await shot(page, "3-inspector-evidence");

    await page.getByRole("link", { name: /in the trace/ }).click();
    await expect(page.locator(".span-detail")).toBeVisible();
    await setTheme(page);
    await shot(page, "4-episode-trace");

    await page.goto("/evals?view=cards");
    await setTheme(page);
    await expect(page.locator("#card-D2")).toBeVisible();
    await page.locator("#card-D2").scrollIntoViewIfNeeded();
    await shot(page, "5-gate-cards");
  });
}
