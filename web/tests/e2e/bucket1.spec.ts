import { expect, test } from "@playwright/test";

// Bucket 1 on /evals: B1 to B9 in order from the shared gate-results route. A gate with no stored result reads
// "Not measured", never a pass.
test("Bucket 1 lists B1 to B9 in order, not measured without a stored result, never a pass", async ({ page }) => {
  await page.goto("/evals");
  const section = page.locator("#bucket-context_intelligence");
  await expect(section).toBeVisible();
  for (const gate of ["B1", "B2", "B3", "B4", "B5", "B6", "B7", "B8", "B9"]) {
    await expect(section.locator(`#gate-${gate}`)).toBeVisible();
  }
  await expect(section.locator("li.gate-row")).toHaveCount(9);
  await expect(section).toContainText("Did gtm_ai understand the new event correctly?");
  await expect(section).not.toContainText("Ghost");
});
