import { expect, test } from "@playwright/test";

// HAR-149 eval inspector: open an episode, follow its path, click D3 and read the nine sections, see the three options and
// the edit's lineage. The fixture core serves the MedTech episode's stored results (tests/e2e/fixture-inspector.mjs).
const MEDTECH = "0e9ead00-0000-4000-8000-00000000ad0a";
const UNMEASURED = "0de50000-0000-4000-8000-000000000202"; // a replayed episode with no stored gate result

test("the loop landing shows the three buckets as questions, with modes as filters", async ({ page }) => {
  await page.goto("/evals/loop");
  await expect(page.getByRole("heading", { name: "Do we understand what is happening?" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Given what we know, are we doing the right thing?" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Can we trust the machinery measuring the first two?" })).toBeVisible();
  await expect(page.getByTestId("loop-feedback")).toContainText("feeds back into Bucket 1");
  const filters = page.getByRole("group", { name: "Show checks by when they run" });
  await filters.getByRole("button", { name: /^Offline/ }).click();
  await expect(page.locator(".loop-bucket[data-bucket='3'] .loop-gates li")).toHaveCount(4);
  await expect(page.locator(".loop-bucket[data-bucket='1'] .loop-gates li")).toHaveCount(0);
});

test("pick an episode, follow the path, click D3: the drawer has the nine sections in order", async ({ page }) => {
  await page.goto("/evals/episode");
  await expect(page.getByTestId("test-data-badge")).toHaveText("TEST DATA");
  await page.getByRole("link", { name: /Fatoumata asked about the long-term cost/ }).click();
  await expect(page).toHaveURL(new RegExp(`/evals/episode/${MEDTECH}$`));
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Sample episode (test data)");

  const path = page.getByRole("list", { name: /The checks of this episode/ });
  await expect(path.locator(".path-node").first()).toContainText("Did gtm_ai record exactly what happened in the new event?");
  await expect(path.getByTestId("path-edit")).toContainText("The human edited the draft");
  await expect(path.getByTestId("path-edit")).toContainText("The recompute record says it made these stale and re-derived them");

  await path.locator(".path-node[data-gate='D3']").click();
  const drawer = page.getByTestId("eval-drawer");
  await expect(drawer).toBeVisible();
  await expect(drawer.getByRole("heading", { level: 2 })).toHaveText("Did gtm_ai recommend the most defensible option, for the stated reasons?");
  await expect(drawer.getByRole("heading", { level: 3 })).toHaveText([
    "What are we doing?",
    "Why do we care?",
    "When does this run?",
    "What went into it?",
    "How was the result produced?",
    "What happened in this run?",
    "What did this result do?",
    "How is it performing over time?",
    "Common failures",
  ]);
  // Per-criterion rows, then the folded result.
  const how = drawer.locator("[data-section='how']");
  await expect(how).toContainText("Supported by the evidence and state");
  await expect(how).toContainText("Uncertainty is reflected");
  await expect(how.locator(".dr-fold")).toContainText("WARN");
  // The effect is stated honestly: a monitoring-only gate only records.
  await expect(drawer.locator("[data-section='effect']")).toContainText("RECORD ONLY");
  await expect(drawer.locator("[data-section='effect']")).toContainText("Nothing in the backend reads this result");
  // Not measured stays not measured.
  await expect(drawer.locator("[data-section='performance']")).toContainText("Not measured yet");
  await expect(drawer.locator("[data-section='performance']")).toContainText("needs the calibration run");
  // The stored ranking is an input, in words; raw JSON only under Advanced.
  await expect(drawer.locator("[data-section='inputs']")).toContainText("Booking the call answers the buyer's request");
  await expect(drawer.locator(".dr-advanced pre")).toBeHidden();

  await page.keyboard.press("Escape");
  await expect(drawer).toHaveCount(0);
});

test("a gate in the path opens from ?gate= and Demo mode hides the raw JSON and the operator link", async ({ page }) => {
  await page.goto(`/evals/episode/${MEDTECH}?gate=B5&demo=1`);
  const drawer = page.getByTestId("eval-drawer");
  await expect(drawer).toContainText("Has a materially similar situation happened before here?");
  await expect(drawer.locator("[data-section='what']")).toContainText("We look at the earlier cases");
  await expect(drawer.locator("[data-section='happened']")).toContainText("relevant earlier case about onboarding fees");
  await expect(drawer.locator(".dr-advanced")).toHaveCount(0);
  await expect(page.getByRole("link", { name: "All gate results" })).toHaveCount(0);
});

test("the three options: recommended, chosen, per-criterion verdicts worst first, and why A won", async ({ page }) => {
  await page.goto(`/evals/episode/${MEDTECH}/decision`);
  const opts = page.getByTestId("candidates").locator(".opt");
  await expect(opts).toHaveCount(3);
  await expect(opts.nth(0)).toContainText("Recommended");
  await expect(opts.nth(1)).toContainText("Chosen");
  await expect(opts.nth(0)).toContainText("Book the call, bring the cost model");
  // Option C: its worst criterion leads.
  const c = opts.nth(2).locator(".opt-grid li").first();
  await expect(c).toContainText("FAIL");
  await expect(c).toContainText("Facts, numbers and dates are right");
  const why = page.getByTestId("why-won");
  await expect(why.getByRole("heading", { name: "Why A won" })).toBeVisible();
  await expect(why).toContainText("Option A above Option B");
  await expect(page.getByTestId("human-choice")).toContainText("The human chose Option B");
  await expect(page.getByTestId("human-edit")).toContainText("The message that went out, checked again");
  await expect(page.getByTestId("human-edit")).not.toContainText("re-run after the edit");
  await expect(page.locator(".dec-note")).toContainText("verdicts, not scores");
});

test("the edit lists only what the recompute record says it re-derived; no lineage is invented", async ({ page }) => {
  await page.goto(`/evals/episode/${MEDTECH}`);
  const edit = page.getByTestId("path-edit");
  await expect(edit.locator(".path-edit-chip").first()).toBeVisible();
  const d8 = page.locator(".path-node[data-gate='D8']");
  await expect(d8).not.toContainText("Recomputed");
  await d8.click();
  const drawer = page.getByTestId("eval-drawer");
  await expect(drawer.locator("[data-section='effect']")).toContainText("RECORD ONLY");
  await expect(drawer.locator("[data-section='effect']")).not.toContainText("replaced an earlier run");
  await expect(drawer.locator("[data-section='how']")).toContainText("Model calls: not recorded");
  await expect(drawer).not.toContainText("fixture-judge");
});

test("an episode with no stored gate results shows nothing as passed", async ({ page }) => {
  await page.goto(`/evals/episode/${UNMEASURED}`);
  const d8 = page.locator(".path-node[data-gate='D8']");
  await expect(d8).not.toContainText("Passed");
  await expect(d8).toContainText("No result recorded");
  await expect(page.locator(".path-item[data-verdict='pass']")).toHaveCount(0);
  await d8.click();
  await expect(page.getByTestId("eval-drawer")).toContainText("It is not a pass");
});

test("offline checks say Not run yet with the deviations; health says not measured without usage", async ({ page }) => {
  await page.goto("/evals/offline");
  await expect(page.getByTestId("offline-headline")).toContainText("None of these has run yet");
  await expect(page.locator(".agg-chip")).toHaveCount(4);
  await expect(page.locator(".agg-item").first()).toContainText("Not run yet");
  await expect(page.getByText(/not human labels/i)).toBeVisible();
  await expect(page.getByText(/One trial per measurement/i)).toBeVisible();

  await page.goto("/evals/health");
  await expect(page.getByRole("heading", { name: "Are the traces complete?" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "The agent versus the checks that watch it" })).toBeVisible();
  await expect(page.locator(".agg-grid dd").first()).toBeVisible();
});

test("the old evals page links to the loop", async ({ page }) => {
  await page.goto("/evals");
  await page.getByRole("link", { name: "The loop" }).click();
  await expect(page).toHaveURL(/\/evals\/loop$/);
});
