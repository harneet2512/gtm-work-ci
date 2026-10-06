import { expect, test } from "@playwright/test";

const ACME_RUN = "0f0a0000-0000-4000-8000-000000000601";
const REPLAY_RUN = "0a120000-0000-4000-8000-0000000000e5";
const MEDTECH_RUN = "0f0aad00-0000-4000-8000-00000000ad60";
const PAGE = "/evals?view=results";

test("the results explorer lists the eval runs with their real area counts", async ({ page }) => {
  await page.goto(PAGE);
  const table = page.getByRole("table").first();
  await expect(table.getByRole("row")).toHaveCount(1 + 3); // header + the three runs that have results
  await expect(table).toContainText("Acme Corp");
  await expect(table).toContainText("MedTech Advances");
  // Real tallies per area, "not measured" where nothing was recorded; no percentage and no score.
  await expect(table).toContainText("3 pass · 2 warn · 2 fail · 0 unknown");
  await expect(table).toContainText("not measured");
  await expect(page.locator("main")).not.toContainText(/%|score/i);
  await expect(page.getByText("Select a run to see its areas, families and eval types.")).toBeVisible();
});

test("selecting a run opens its areas, families and eval types, with deltas labelled previous episode", async ({ page }) => {
  await page.goto(PAGE);
  await page.getByRole("row").filter({ hasText: "Acme Corp" }).first().getByRole("link").click();
  await expect(page).toHaveURL(/run=/);
  const decision = page.getByRole("region", { name: "Decision & Learning results" });
  await expect(decision).toContainText("3 pass · 2 warn · 2 fail · 0 unknown");
  await expect(decision).toContainText("Decision construction");
  await expect(page.getByRole("region", { name: "Intelligence results" })).toContainText("not measured");
  await expect(page.getByRole("region", { name: "Intelligence results" })).not.toContainText("vs previous episode");
  await expect(decision).toContainText("Action / output generation");
  await expect(page.getByRole("region", { name: "Cliff / Experience results" })).toContainText("not measured");
  // The inspector summarises the selected run and links its evals and its episode.
  await expect(page.locator(".inspector")).toContainText("Acme Corp");
  await expect(page.locator(".inspector").getByRole("link", { name: /evals with their reasons/ })).toHaveAttribute("href", /\/runs\/.*\/evals$/);
});

test("a run with a previous episode shows its delta against that episode", async ({ page }) => {
  await page.goto(`${PAGE}&run=${REPLAY_RUN}`);
  await expect(page.getByRole("region", { name: "Decision & Learning results" })).toContainText("vs previous episode: no change");
});

test("a run with no previous episode says so instead of a delta", async ({ page }) => {
  await page.goto(`${PAGE}&run=${MEDTECH_RUN}`);
  await expect(page.getByRole("region", { name: "Decision & Learning results" })).toContainText("no previous episode to compare");
});

test("an unknown run id says it has no recorded eval results", async ({ page }) => {
  await page.goto(`${PAGE}&run=0f0a0000-0000-4000-8000-0000000000ff`);
  await expect(page.getByText("That run has no recorded eval results.")).toBeVisible();
});

test("the operator comparison puts two runs of one trigger on the same eval axes", async ({ page }) => {
  await page.goto(`/evals?view=compare&a=${ACME_RUN}&b=${REPLAY_RUN}`);
  await expect(page.getByText(/Operator view/)).toBeVisible();
  await expect(page.getByText("Whole episode:")).toContainText("unchanged");
  const table = page.getByRole("table").first();
  await expect(table.getByRole("row").nth(1)).toBeVisible();
  await expect(table).toContainText("unchanged");
});

test("runs of different triggers are refused in words, not drawn as a failure", async ({ page }) => {
  await page.goto(`/evals?view=compare&a=${ACME_RUN}&b=${MEDTECH_RUN}`);
  await expect(page.locator("main .notices")).toContainText("cannot be compared");
  await expect(page.getByRole("table")).toHaveCount(0);
});

test("the comparison picker submits two runs", async ({ page }) => {
  await page.goto("/evals?view=compare");
  await page.getByLabel("Run A").selectOption(ACME_RUN);
  await page.getByLabel("Run B").selectOption(REPLAY_RUN);
  await page.getByRole("button", { name: "Compare" }).click();
  await expect(page).toHaveURL(new RegExp(`a=${ACME_RUN}&b=${REPLAY_RUN}`));
  await expect(page.getByText("Whole episode:")).toBeVisible();
});

test("the catalog view keeps its tab with the view tabs, and the comparison is labelled operator", async ({ page }) => {
  await page.goto("/evals?view=catalog");
  await expect(page.getByRole("heading", { name: "How gtm_ai checks its work" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Eval views" })).toContainText("Run comparison (operator)");
  await page.getByRole("link", { name: "Results", exact: true }).click();
  await expect(page).toHaveURL(/view=results/);
});
