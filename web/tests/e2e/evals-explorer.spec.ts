import { expect, test } from "@playwright/test";

const PAGE = "/evals?view=results";

test("the results explorer lists produced eval results and filters them", async ({ page }) => {
  await page.goto(PAGE);
  const table = page.getByRole("table");
  const all = await table.getByRole("row").count();
  expect(all).toBeGreaterThan(20); // several fixture runs' bundles — never a "not checked" phantom row
  await expect(table).toContainText("CTA calibration");
  await expect(page.getByText(/\d+ of \d+ results/)).toBeVisible();

  await page.getByLabel("Search eval results").fill("cta");
  const filtered = await table.getByRole("row").count();
  expect(filtered).toBeGreaterThan(1);
  expect(filtered).toBeLessThan(all);

  await page.getByLabel("Search eval results").fill("");
  await page.getByRole("button", { name: "✗ fail" }).click();
  await expect(table.getByRole("row").first()).toBeVisible();
  expect(await table.getByRole("row").count()).toBeGreaterThan(1);
});

test("sorting a column flips its aria-sort direction", async ({ page }) => {
  await page.goto(PAGE);
  const head = page.getByRole("columnheader").filter({ has: page.getByRole("button", { name: /Eval/ }) });
  await page.getByRole("button", { name: "Eval" }).click();
  await expect(head).toHaveAttribute("aria-sort", "ascending");
  await page.getByRole("button", { name: /Eval/ }).click();
  await expect(head).toHaveAttribute("aria-sort", "descending");
});

test("selecting a row fills the inspector with that result", async ({ page }) => {
  await page.goto(PAGE);
  const row = page.getByRole("table").getByRole("row").nth(1);
  const type = (await row.locator(".etype").textContent())!;
  await row.click();
  await expect(page).toHaveURL(/result=/);
  const insp = page.locator(".inspector");
  await expect(insp).toContainText(new RegExp(type));
  await expect(insp).toContainText(/Fail|Warn|Pass|Unsure|Not relevant/);
});

test("compare view puts two runs on the same eval axes", async ({ page }) => {
  await page.goto("/evals?view=compare");
  await expect(page.getByText("Run A")).toBeVisible();
  await expect(page.getByText("Run B")).toBeVisible();
  await expect(page.getByText(/eval types ·/)).toBeVisible();
  // MedTech and Acme check different eval types — one-sided rows are real, not errors.
  await expect(page.getByRole("table")).toContainText("not checked");
});

test("the catalog view stays the default with the view tabs", async ({ page }) => {
  await page.goto("/evals");
  await expect(page.getByRole("heading", { name: "How Ghost checks its work" })).toBeVisible();
  await page.getByRole("link", { name: "Results" }).click();
  await expect(page).toHaveURL(/view=results/);
});
