import { expect, test } from "@playwright/test";

// The Gates view: one card per gate grouped by bucket; "View results" opens the results table already filtered to the gate.

test("Gates view, card, View results filters the table", async ({ page }) => {
  const errors: string[] = [];
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));

  await page.goto("/evals");
  await page.getByRole("navigation", { name: "Eval views" }).getByRole("link", { name: "Gate cards", exact: true }).click();
  await expect(page).toHaveURL(/view=cards/);
  await expect(page.getByRole("heading", { level: 3, name: "Context" })).toBeVisible();
  await expect(page.locator(".gate-card")).toHaveCount(25);
  await expect(page.locator(".cards-bucket").nth(0).locator(".gate-card")).toHaveCount(9);
  await expect(page.locator(".cards-bucket").nth(1).locator(".gate-card")).toHaveCount(10);

  const d2 = page.locator("#card-D2");
  await expect(d2).toContainText("Three-candidate quality");
  await expect(d2.getByRole("list", { name: /Judges the Candidates step/ })).toBeVisible();
  await expect(d2.locator(".moment")).toHaveText("Play · Cliff M2");
  await expect(d2).toContainText("WARN"); // the fixture's real D2 result
  // Clean by default: the criteria sit behind Details.
  await expect(d2.getByRole("list", { name: "D2 verdict criteria" })).toBeHidden();
  await d2.getByText("Details", { exact: true }).click();
  await expect(d2.getByRole("list", { name: "D2 verdict criteria" }).getByRole("listitem")).toHaveCount(4);
  await expect(d2).toContainText("Catches:");
  await expect(d2).toContainText("not yet calibrated");
  await expect(page.locator("#card-D3")).toContainText("Runs at Play · Cliff M2");
  await expect(page.locator("#card-B3")).toContainText("Runs when you press Play");
  await expect(page.locator("#card-B1")).toContainText("PASS"); // the MedTech episode's real B1 result
  await expect(page.locator("#card-B1 .result-summary cite")).toContainText(/^Summary/); // a recorded summary, not a verbatim quote
  await expect(page.locator("#card-B1")).toContainText("not yet calibrated"); // visible without expanding anything
  await expect(page.locator("#card-B1 .mode-badge")).toHaveText("Live");
  await expect(page.locator("#card-D5")).toContainText("Not triggered — no human edit in this episode");
  await expect(page.locator("#card-D5 .mode-badge")).toHaveText("Conditional");
  await expect(page.locator("#card-S2")).toContainText("Offline benchmark — runs on a fixed set of test cases, not on this episode");
  await expect(page.locator("#card-S6")).toContainText("Continuous");
  await expect(page.locator(".loop-back")).toBeVisible();

  await d2.getByRole("link", { name: "View results for D2" }).click();
  await expect(page).toHaveURL(/view=gates&gate=D2/);
  await expect(page.getByRole("combobox", { name: "Gate" })).toHaveValue("D2");
  const rows = page.locator("tbody tr[data-gate]");
  await expect(rows.first()).toBeVisible();
  for (const r of await rows.all()) await expect(r).toHaveAttribute("data-gate", "D2");

  // The inspector's Result tab carries the gate definition from the registry.
  await rows.first().click();
  await expect(page.getByRole("region", { name: "Gate definition" })).toContainText("Catches:");
  await expect(page.locator("main")).not.toContainText("Ghost");
  expect(errors).toEqual([]);
});

test("cards drop to one column on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/evals?view=cards");
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  const cols = await page.locator(".card-grid").first().evaluate((el) => getComputedStyle(el).gridTemplateColumns.split(" ").length);
  expect(cols).toBe(1);
});
