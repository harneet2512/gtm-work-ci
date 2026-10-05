import { expect, test } from "@playwright/test";

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";

test("system shows the four honest health reads", async ({ page }) => {
  await page.goto("/system");
  await expect(page.getByRole("heading", { name: "System" })).toBeVisible();

  // Context bar: core ok, breaker closed, slack outbox drained, held-out check not asked yet.
  await expect(page.locator(".context-bar")).toContainText("Provider breaker");
  await expect(page.locator(".context-bar")).toContainText("closed");
  await expect(page.locator(".context-bar")).toContainText("0 unacked");
  await expect(page.locator(".context-bar")).toContainText("not checked");

  // The breaker and outbox panels state the fixture's steady state.
  await expect(page.getByRole("heading", { name: "Provider breaker" })).toBeVisible();
  await expect(page.getByText(/Closed — no provider failures counted/)).toBeVisible();
  await expect(page.getByText("Consumer drained — nothing unacknowledged")).toBeVisible();

  // Without a manifest the leak check is not asked — the page says so and offers the form.
  await expect(page.getByText(/Not checked — give a manifest id/)).toBeVisible();
  await page.getByLabel("Demo manifest id").fill(MANIFEST);
  await page.getByRole("button", { name: "Check leak" }).click();
  await page.waitForURL(/\/system\?manifest=/);
  await expect(page.getByText(/Withheld — 2 stores inspected, nothing leaked/)).toBeVisible();
  await expect(page.getByText("postgres, neo4j")).toBeVisible();
});

test("system is on the rail", async ({ page }) => {
  await page.goto("/system");
  const rail = page.getByRole("navigation", { name: "Sections" });
  const link = rail.getByRole("link", { name: "System" });
  await expect(link).toHaveAttribute("aria-current", "page");
});
