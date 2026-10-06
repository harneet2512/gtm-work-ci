import { expect, test } from "@playwright/test";

// PR #66 review MEDIUM 8: at 390 px the document must not scroll sideways. Wide tables scroll inside their own
// labelled frame (ScrollTable) instead of widening the page.
const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const EPISODE = "0e9ead00-0000-4000-8000-00000000ad0a";

test.use({ viewport: { width: 390, height: 844 } });

const PAGES = [
  ["control", `/control?manifest=${MANIFEST}`],
  ["episode story", `/episodes/${EPISODE}`],
  ["episode trace", `/episodes/${EPISODE}?mode=trace`],
  ["episode graph diff", `/episodes/${EPISODE}?mode=graphdiff`],
  ["eval results", "/evals?view=results"],
  ["eval compare", "/evals?view=compare"],
  ["system", "/system"],
] as const;

for (const [name, url] of PAGES) {
  test(`${name} does not overflow horizontally at 390 px`, async ({ page }) => {
    await page.goto(url);
    await expect(page.locator("main, .page").first()).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(overflow, `${url} is ${overflow}px wider than the viewport`).toBeLessThanOrEqual(0);
  });
}

test("a wide table is a focusable labelled scroll region", async ({ page }) => {
  await page.goto("/evals");
  const region = page.getByRole("region", { name: "Gate results, scrollable" });
  await expect(region).toBeVisible();
  await region.focus();
  await expect(region).toBeFocused();
});
