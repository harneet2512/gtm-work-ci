import { expect, test } from "@playwright/test";

// /explaining-evals (HAR-97 eval list, HAR-149): every eval explained in plain words, in the order of the product loop.
const SECTIONS = ["Message 1", "Message 2", "Message 3", "The next similar case", "Keeping the evals honest"];

test("the sections follow the product loop and all 29 cards render", async ({ page }) => {
  await page.goto("/explaining-evals");
  await expect(page.getByRole("heading", { level: 1, name: "Explaining evals" })).toBeVisible();
  const kickers = await page.locator(".xsection-kicker").allTextContents();
  expect(kickers.map((k) => k.replace(/^\d+/, "").trim())).toEqual(SECTIONS);
  await expect(page.getByTestId("explainer-card")).toHaveCount(29);
  await expect(page.locator('[data-soon="true"]')).toHaveCount(4);
  await expect(page.getByText("Coming soon")).toHaveCount(4);
  await expect(page.getByRole("heading", { level: 2 }).filter({ hasText: "The six answers an eval can give" })).toBeVisible();
  for (const label of ["Passed", "Warning", "Failed", "Couldn't tell", "Didn't run", "Doesn't apply"]) await expect(page.locator(".xresults").getByText(label, { exact: true })).toBeVisible();
});

test("no eval id, snake_case or old brand is on the page", async ({ page }) => {
  await page.goto("/explaining-evals");
  const text = await page.locator("main").innerText();
  expect(text.match(/\b[BDS]\d{1,2}\b/)).toBeNull();
  expect(text.match(/\bM[123]\b/)).toBeNull();
  expect(text.match(/\b(?!gtm_ai\b)[a-z]+(?:_[a-z0-9]+)+\b/)).toBeNull();
  expect(text).not.toMatch(/\bGhost\b/);
});

test("every card says when it starts and what happens with the result", async ({ page }) => {
  await page.goto("/explaining-evals");
  const cards = page.getByTestId("explainer-card");
  for (let i = 0; i < 29; i++) {
    const c = cards.nth(i);
    for (const label of ["What it checks", "Why we need it", "For example", "When it starts", "What happens with the result", "How it decides"]) await expect(c.getByText(label, { exact: true })).toHaveCount(1);
  }
});

test("is reachable from the rail and from the eval pages", async ({ page }) => {
  await page.goto("/evals/loop");
  await page.getByRole("navigation", { name: "Eval inspector" }).getByRole("link", { name: "Explaining evals" }).click();
  await expect(page).toHaveURL(/\/explaining-evals$/);
  await expect(page.getByRole("navigation", { name: "Sections" }).getByRole("link", { name: "Explaining evals" })).toHaveAttribute("aria-current", "page");
});

for (const [name, width, height] of [["1440", 1440, 900], ["1280", 1280, 800], ["phone", 390, 844]] as const) {
  test(`does not overflow sideways at ${name}`, async ({ page }) => {
    await page.setViewportSize({ width, height });
    await page.goto("/explaining-evals");
    await expect(page.getByTestId("explainer-card").first()).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });
}
