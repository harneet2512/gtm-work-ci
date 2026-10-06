import { expect, test } from "@playwright/test";

// Home and /runs read the served lists (cursor pages). The home page used to return a 500 when the core had no accounts
// list; it now lists them, and a refused cursor or filter is said in words instead of being shown as an empty list.

test("home lists the accounts the core serves", async ({ page }) => {
  const res = await page.goto("/");
  expect(res?.status()).toBe(200);
  await expect(page.getByRole("heading", { level: 1, name: "Accounts" })).toBeVisible();
  const list = page.locator(".account-list");
  await expect(list).toContainText("Acme Corp");
  await expect(list).toContainText("MedTech Advances");
  await expect(page.getByRole("link", { name: "More accounts" })).toHaveCount(0); // one page: no next cursor
});

test("home follows a cursor to the next page of accounts", async ({ page }) => {
  await page.goto("/?cursor=o1");
  const list = page.locator(".account-list");
  await expect(list).toContainText("MedTech Advances");
  await expect(list).not.toContainText("Acme Corp");
});

test("a page marker the core refuses falls back to the first accounts, and says so", async ({ page }) => {
  await page.goto("/?cursor=junk");
  await expect(page.getByText("That page marker was not valid, so the first accounts are shown.")).toBeVisible();
  await expect(page.locator(".account-list")).toContainText("Acme Corp");
});

test("runs lists the decision runs and filters them by status", async ({ page }) => {
  await page.goto("/runs");
  const table = page.getByRole("table");
  await expect(table.getByRole("row")).toHaveCount(1 + 4); // header + the four fixture runs
  await page.goto("/runs?status=awaiting_human");
  await expect(page.getByRole("table").getByRole("row").nth(1)).toContainText("awaiting_human");
});

test("a status the core refuses is said, not shown as no runs", async ({ page }) => {
  await page.goto("/runs?status=bogus");
  await expect(page.getByText("That filter was not accepted, so no runs are shown for it.")).toBeVisible();
  await expect(page.getByText("No agent runs yet.")).toHaveCount(0);
});

test("runs follows a refused cursor back to the newest runs", async ({ page }) => {
  await page.goto("/runs?cursor=junk");
  await expect(page.getByText("That page marker was not valid, so the newest runs are shown.")).toBeVisible();
  await expect(page.getByRole("table").getByRole("row").nth(1)).toBeVisible();
});
