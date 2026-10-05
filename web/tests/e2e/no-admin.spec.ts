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
