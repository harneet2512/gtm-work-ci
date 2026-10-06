import { expect, test } from "@playwright/test";

// The Braintrust-style eval surface: /evals opens on the gate results table; a row opens the inspector without navigating
// away; an evidence link lands on the episode trace with that span selected. Both buckets come from one route.

const MEDTECH_EPISODE_PREFIX = "MedTech";

test("open row, inspector, evidence link, trace span", async ({ page }) => {
  const errors: string[] = [];
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/evals");
  await expect(page.getByRole("heading", { name: "Gate results" })).toBeVisible();
  await expect(page.getByRole("list", { name: "Score summary by bucket" }).getByRole("listitem")).toHaveCount(3);

  // Failures and warnings first: the first measured row is a WARN or FAIL, never a PASS.
  const table = page.getByRole("table");
  const b1 = table.locator("tbody tr[data-gate='B1'][data-measured='true']");
  await expect(b1).toHaveCount(1);
  await expect(b1).toContainText("PASS");
  await expect(table.locator("tbody tr").first()).not.toContainText(/^.*PASS/);

  // Gates with no stored result read "not measured", never a pass.
  await expect(table.locator("tbody tr[data-measured='false']")).toHaveCount(0); // folded: measured results lead
  await page.getByRole("button", { name: /gates with no result yet/ }).click();
  const nm = table.locator("tbody tr[data-measured='false']").first();
  await expect(table.locator("tbody tr[data-gate='D5']").first()).toContainText("NOT APPLICABLE"); // a conditional gate whose trigger did not occur
  await expect(table.locator("tbody tr[data-gate^='S']")).toHaveCount(0); // offline and continuous gates are not per-episode rows
  await expect(table.locator("tbody tr[data-gate='D2'][data-measured='true']")).toContainText("uncalibrated"); // visible in the verdict cell
  await expect(nm).toContainText(/NOT (RUN|APPLICABLE)/);

  // Open a row: the inspector appears beside the table and the URL does not change.
  const url = page.url();
  const row = table.locator("tbody tr[data-gate='B1'][data-measured='true']");
  await row.focus();
  await page.keyboard.press("Enter");
  const inspector = page.getByRole("complementary", { name: "Inspector for B1" });
  await expect(inspector).toBeVisible();
  expect(page.url()).toBe(url);
  await expect(inspector).toContainText("Did gtm_ai understand the new event correctly?");

  // Evidence tab: the real record, a link into the trace.
  await inspector.getByRole("tab", { name: /Evidence/ }).click();
  const link = inspector.getByRole("link", { name: /in the trace/ });
  await expect(link).toBeVisible();
  await link.click();

  // The trace opens with that span selected and its evaluator attached.
  await expect(page).toHaveURL(/\/episodes\/[0-9a-f-]+\?mode=trace&span=source_event/);
  const detail = page.locator(".span-detail");
  await expect(detail).toHaveAttribute("data-span", /^source_event:/);
  await expect(page.locator(".span-btn[aria-current='true']")).toHaveCount(1);
  // One aggregated pill with counts on the span row, no multi-year gap, a human title with the id beside a copy button.
  await expect(page.locator(".span-btn[aria-current='true'] .span-agg")).toContainText("1 warn · 1 pass");
  await expect(page.locator(".span-btn[aria-current='true'] .verdict")).toHaveCount(1);
  await expect(page.locator(".span-gap", { hasText: /\d{3,} d/ })).toHaveCount(0);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(/^MedTech Advances · .* · Nov 9, 2023$/);
  await expect(page.getByRole("button", { name: "Copy episode id" })).toBeVisible();
  await detail.getByRole("tab", { name: /Evaluators \(2\)/ }).click();
  await expect(detail).toContainText("B1");
  await expect(detail).toContainText("PASS");
  await expect(detail).toContainText("WARN");
  expect(errors).toEqual([]);
  expect(MEDTECH_EPISODE_PREFIX).toBe("MedTech");
});

test("Esc closes the inspector, filters narrow the table, Ctrl+K focuses search", async ({ page }) => {
  await page.goto("/evals");
  await page.locator("tbody tr[data-measured='true']").first().click();
  await expect(page.getByRole("complementary", { name: /Inspector for/ })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("complementary", { name: /Inspector for/ })).toHaveCount(0);

  await page.getByRole("button", { name: "WARN", exact: true }).click();
  const rows = page.locator("tbody tr[data-gate]");
  await expect(rows.first()).toContainText("WARN");
  for (const r of await rows.all()) await expect(r).toContainText("WARN");

  await page.keyboard.press("Control+k");
  await expect(page.getByRole("searchbox", { name: "Search gate results" })).toBeFocused();
  await expect(page.locator("main")).not.toContainText("Ghost");
});

test("the episode trace is a span tree with detail, and the old modes remain", async ({ page }) => {
  await page.goto("/evals");
  await page.locator("tbody tr[data-gate='B1'][data-measured='true']").click();
  await page.getByRole("tab", { name: /Evidence/ }).click();
  await page.getByRole("link", { name: /in the trace/ }).click();
  await expect(page.getByRole("navigation", { name: "Span tree" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Trace modes" })).toContainText("Graph diff");
  await page.getByRole("button", { name: "Raw JSON" }).click();
  await expect(page.locator(".raw-json")).toContainText("source_event");
});
