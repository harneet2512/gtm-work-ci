import { expect, test } from "@playwright/test";

// Product-owner rule: the audience sees the Web Control Plane and Cliff in Slack, nothing else, and Play is the only visible
// trigger. There is no /admin page, the shared header carries no demo status, and the System page has no operator section
// (no Reset, no Continue, no service list, no model-call counts).
const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";

test("there is no admin page", async ({ page }) => {
  const res = await page.goto("/admin");
  expect(res?.status()).toBe(404);
});

test("the header and the system page show no demo status or developer text", async ({ page }) => {
  const problems: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error") problems.push(`console: ${m.text()}`);
  });
  page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));

  await page.goto(`/control?manifest=${MANIFEST}&demo=1`);
  await expect(page.locator("header.top")).not.toContainText(/replaying|recording|spend|systems ready/i);
  await expect(page.getByTestId("demo-llm")).toHaveCount(0);

  await page.goto("/system");
  await expect(page.getByRole("heading", { name: "System" })).toBeVisible();
  await expect(page.getByTestId("demo-operator")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /reset|continue/i })).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText(/ghostctl|secret|codespace|replaying recorded|recording new call|all systems ready/i);
  expect(problems).toEqual([]);
});

// Brand rule: the product is named gtm_ai wherever the audience looks. The browser title and the rendered text of the
// main pages never say "Ghost" (internal ids such as ghost_worker are never rendered).
test("the browser title and the rendered pages carry the gtm_ai name, not the old one", async ({ page }) => {
  for (const url of ["/", "/evals", "/system", `/control?manifest=${MANIFEST}&demo=1`]) {
    await page.goto(url);
    expect(await page.title(), `${url} title`).not.toMatch(/\bghost\b/i);
    await expect(page.locator("body"), url).not.toContainText(/\bghost\b/i);
    await expect(page.locator("body"), `${url} seller domain`).not.toContainText(/ghostvendor/i);
  }
  await page.goto("/");
  expect(await page.title()).toContain("gtm_ai");
  await expect(page.locator("aside.rail .brand")).toContainText("gtm_ai");
});
