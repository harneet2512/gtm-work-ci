import { expect, test } from "@playwright/test";

// WP24 (HAR-122): the run detail page answers, from the UI alone — what did the agent decide, why
// (evals and evidence), what did the human change, and what did the system learn. The fixtures are
// the contract examples plus the recorded Acme chain, so every rendered number is real.

const RUN = "0f0a0000-0000-4000-8000-000000000601";
const ACCOUNT = "0a0c0000-0000-4000-8000-000000000001";
const K17 = "0c17c000-0000-4000-8000-000000000017";

test("the runs list links into the run chain", async ({ page }) => {
  await page.goto("/runs");
  await page.getByRole("link", { name: "0f0a0000" }).click();
  await expect(page).toHaveURL(new RegExp(`/runs/${RUN}$`));
  await expect(page.getByRole("heading", { level: 1, name: "Run 0f0a0000" })).toBeVisible();
});

test("the run page renders the full decision chain", async ({ page }) => {
  await page.goto(`/runs/${RUN}`);

  // What triggered it: the eligibility check, the diff and the signals behind the run.
  const trigger = page.getByRole("region", { name: "What triggered this run" });
  await expect(trigger.getByText(/eligible/)).toBeVisible();
  await expect(trigger.getByText(/version 6 → 7/)).toBeVisible();

  // What gtm_ai decided: three candidates, the preference and the human's pick, evals one by one.
  const candidates = page.getByRole("region", { name: "Candidates gtm_ai evaluated" });
  await expect(candidates.getByRole("article")).toHaveCount(3);
  await expect(candidates.getByText("gtm_ai's pick")).toBeVisible();
  await expect(candidates.getByText("chosen by human")).toBeVisible();
  // The preferred draft's failed evals (CTA calibration, and Relationship continuity: Priya dropped from cc)
  // read in plain words with their reasons; a line expands to why the eval applies and its evidence.
  const preferred = candidates.getByRole("article").first();
  await expect(preferred.getByText("2 fail")).toBeVisible();
  await expect(preferred.getByText(/cannot commit to a review date/).first()).toBeVisible();
  await preferred.getByText("CTA calibration").first().click();
  await expect(preferred.getByText("Why this eval applies").first()).toBeVisible();
});

test("Why did this action change? answers with the recorded delta, never a guess", async ({ page }) => {
  await page.goto(`/runs/${RUN}`);
  const why = page.getByRole("region", { name: "Why did this action change?" });

  // The override: gtm_ai preferred the dated-ask draft, the human chose the documents-first one.
  await expect(why.getByText(/gtm_ai preferred “.*”; the human chose “.*”/)).toBeVisible();
  // The eval-verdict difference between the two sides, from the judgment inference.
  await expect(why.getByText("CTA calibration", { exact: true })).toBeVisible();
  // The inferred semantic delta and the human's verdict on it.
  await expect(why.getByText("Semantic delta")).toBeVisible();
  await expect(why.getByText(/corrected by the human/)).toBeVisible();

  // The knowledge comparison cites real objects and links through to their pages.
  await why.getByRole("link", { name: /K17/ }).click();
  await expect(page).toHaveURL(new RegExp(`/knowledge/${K17}$`));
  await expect(page.getByRole("heading", { level: 1, name: "K17" })).toBeVisible();
});

test("the human decision and the learning placeholders are honest about what exists", async ({ page }) => {
  await page.goto(`/runs/${RUN}`);
  const human = page.getByRole("region", { name: "Human decision" });
  await expect(human.getByText(/Dana Kim/).first()).toBeVisible();
  await expect(human.getByText("cta_changed")).toBeVisible();
  await expect(human.getByText(/let us know what timing suits you/).first()).toBeVisible();

  // Populated placeholder: the customer's positive reply; empty ones say WP21/WP22 fill them.
  const reaction = page.getByRole("region", { name: "Customer reaction" });
  await reaction.locator("summary").first().click();
  await expect(reaction.getByText(/replied/).first()).toBeVisible();
  await expect(page.getByText(/No eval runs recorded here yet/)).toBeVisible();
  await expect(page.getByText(/No knowledge update recorded yet/)).toBeVisible();
});

test("the run chain degrades instead of 500ing when a section is missing", async ({ page }) => {
  // The second fixture run has a document but no trace, strategies or decision (fixture 404s).
  const res = await page.goto("/runs/0a120000-0000-4000-8000-000000000002");
  expect(res?.status()).toBe(200);
  await expect(page.getByText(/No strategy set recorded for this run/)).toBeVisible();
  await expect(page.getByText(/No human decision recorded/)).toBeVisible();
  await expect(page.getByText(/No human decision yet/)).toBeVisible();
});

test("an unknown run is a 404 page, not a crash", async ({ page }) => {
  const res = await page.goto("/runs/0f0a0000-0000-4000-8000-00000000ffff");
  expect(res?.status()).toBe(404);
});

test("the knowledge list filters by lifecycle status and links into the detail", async ({ page }) => {
  await page.goto("/knowledge");
  await expect(page.getByRole("heading", { level: 1, name: "Knowledge" })).toBeVisible();
  await expect(page.getByRole("link", { name: "K17" })).toBeVisible();
  await expect(page.getByRole("link", { name: "K23" })).toBeVisible();

  await page.getByLabel("Lifecycle status").selectOption("provisional");
  await page.getByRole("button", { name: "Filter" }).click();
  await expect(page).toHaveURL(/status=provisional/);
  await expect(page.getByRole("link", { name: "K23" })).toBeVisible();
  await expect(page.getByRole("link", { name: "K17" })).toHaveCount(0);

  await page.getByRole("link", { name: "K23" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "K23" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Guidance" })).toBeVisible();
  await expect(page.getByText(/dated, owned next step/)).toBeVisible();
  await expect(page.getByRole("heading", { name: "Lifecycle history" })).toBeVisible();
});

test("a bogus status filter is ignored with a notice, not sent to the core to 400", async ({ page }) => {
  await page.goto("/knowledge?status=archived");
  await expect(page.getByText(/not a lifecycle status/)).toBeVisible();
  await expect(page.getByRole("link", { name: "K17" })).toBeVisible();
});

test("an unknown knowledge id is a 404 page", async ({ page }) => {
  const res = await page.goto("/knowledge/0c17c000-0000-4000-8000-0000000000ff");
  expect(res?.status()).toBe(404);
});

test("the account page shows commitments, the next milestone and the latest agent action", async ({ page }) => {
  await page.goto(`/accounts/${ACCOUNT}`);
  const snap = page.getByRole("region", { name: "Commitments & next step" });
  await expect(snap.getByText("Security review of SOC2 package")).toBeVisible();
  await expect(snap.getByText("AgentActionExecuted")).toBeVisible();
  await expect(snap.getByText(/SOC2 Type II report and pen-test summary/)).toBeVisible();
});
