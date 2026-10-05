import { expect, test } from "@playwright/test";

// How Ghost shows evals (eval design spec 4b-3), end to end against the fixture core: the episode eval page
// (judgment first, eval x option comparison, the chosen action's evals with evidence, the honest after-edit
// state, "This eval is wrong") and the /evals overview read from the contracts.

const RUN = "0f0a0000-0000-4000-8000-000000000601";
const A = "0ca00000-0000-4000-8000-0000000000a1";
const MARCO_EMAIL = "0ac70000-0000-4000-8000-000000000101";
const CTA_B = "0e1a0000-0000-4000-8000-000000009201";

test("the run page links to its eval page, which leads with the judgment", async ({ page }) => {
  await page.goto(`/runs/${RUN}`);
  await page.locator("main").getByRole("navigation", { name: "Sections" }).getByRole("link", { name: "Evals" }).click();
  await expect(page).toHaveURL(new RegExp(`/runs/${RUN}/evals$`));
  await expect(page.getByRole("heading", { level: 1, name: "Acme Corp: how Ghost judged this decision" })).toBeVisible();

  const banner = page.getByRole("region", { name: "Warn: CTA calibration" });
  await expect(banner).toContainText("Marco said he cannot commit to a date");
  await expect(banner).toContainText("Option B · Send package, buyer sets timing · chosen by Dana Kim.");
});

test("the comparison shows every eval across the options, and a column opens that option's evals", async ({ page }) => {
  await page.goto(`/runs/${RUN}/evals`);
  const table = page.getByRole("table", { name: /Every eval Ghost ran, by option/ });
  await expect(table.getByRole("columnheader")).toHaveCount(4);
  await expect(table.getByRole("columnheader").nth(1)).toContainText("Ghost's pick");
  await expect(table.getByRole("columnheader").nth(2)).toContainText("Chosen");
  await expect(table.getByRole("rowheader").first()).toContainText("CTA calibration");
  await expect(table.getByRole("row").nth(1).getByRole("cell").first()).toContainText("Fail");
  await expect(table.getByText("Not checked").first()).toBeVisible();

  await table.getByRole("link", { name: /Propose a security call/ }).click();
  await expect(page).toHaveURL(new RegExp(`/runs/${RUN}/evals\\?candidate=${A}#selected$`));
  const selected = page.getByRole("region", { name: "Evals for option A: Propose a security call" });
  await expect(selected).toBeVisible();
  await expect(selected.getByText("This is not the option Dana Kim chose.")).toBeVisible();
  await expect(selected.locator("li.eval-line h3")).toHaveText(["CTA calibration", "Relationship continuity", "Enough evidence"]);
});

test("each verdict opens its evidence by keyboard, and the evidence links back to the source in the trace", async ({ page }) => {
  await page.goto(`/runs/${RUN}/evals`);
  const line = page.locator(`#result-${CTA_B}`);
  await expect(line.getByRole("heading", { level: 3, name: "CTA calibration" })).toBeVisible();
  await expect(line).toContainText("Sales methodology");

  const evidence = line.locator("details.eval-evidence > summary");
  await evidence.focus();
  await page.keyboard.press("Enter");
  const snippet = line.locator("details.eval-evidence");
  await expect(snippet.getByText("Marco Ruiz")).toBeVisible();
  await expect(snippet.getByRole("time")).toHaveText("Sep 29, 2026");
  await expect(snippet.getByText("“I can't commit to a review date until our team has read them.”")).toBeVisible();

  // Who judged it is the card's quiet meta line, never the main line (the web eval page is the expanded view).
  await expect(line.locator(".eval-line-head")).not.toContainText("AI judge");
  await expect(line.locator(".eval-line-meta")).toContainText("AI judge · deepseek/deepseek-v4-flash");

  await snippet.getByRole("link", { name: "Open the email" }).click();
  await expect(page).toHaveURL(new RegExp(`/runs/${RUN}#activity-${MARCO_EMAIL}$`));
  await expect(page.locator(`#activity-${MARCO_EMAIL}`)).toBeVisible();
});

test("the edited email is honestly not re-evaluated yet, and Not relevant is collapsed", async ({ page }) => {
  await page.goto(`/runs/${RUN}/evals`);
  const after = page.getByRole("region", { name: "Not re-evaluated yet" });
  await expect(after).toContainText("Dana Kim changed the call to action and edited a paragraph.");
  await expect(after).toContainText("not on the edited version");
  await expect(after).toContainText("Warn on CTA calibration");
  const notRelevant = page.locator("details.not-relevant");
  await expect(notRelevant.getByText("Economic buyer")).toBeHidden();
  await notRelevant.locator("summary").click();
  await expect(notRelevant.getByText("Economic buyer")).toBeVisible();
});

test("This eval is wrong records a dispute in the core", async ({ page }) => {
  await page.goto(`/runs/${RUN}/evals`);
  const line = page.locator(`#result-${CTA_B}`);
  await line.getByRole("button", { name: "This eval is wrong" }).click();
  const form = line.getByRole("form", { name: "Dispute CTA calibration" });
  await form.getByLabel("What did the eval get wrong?").fill("Marco asked for the documents first; offering a call once they land is fine.");
  await form.getByLabel("It should have been").selectOption("pass");
  await form.getByRole("button", { name: "Send to eval review" }).click();
  await expect(line.getByRole("status")).toContainText("Disagreement recorded: you said it should be Pass.");
  await expect(line.getByRole("form")).toHaveCount(0);
});

test("a deep link lands on one verdict", async ({ page }) => {
  await page.goto(`/runs/${RUN}/evals#result-${CTA_B}`);
  await expect(page.locator(`#result-${CTA_B}`)).toBeInViewport();
});

test("an unknown run is a 404, not an error page", async ({ page }) => {
  const res = await page.goto("/runs/0a120000-0000-4000-8000-0000000000ff/evals");
  expect(res?.status()).toBe(404);
});

test("the overview lists every check; judge quality sits in the System job as a dated recorded measurement", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("navigation", { name: "Sections" }).getByRole("link", { name: "Evals" }).click();
  await expect(page).toHaveURL(/\/evals$/);
  await expect(page.getByRole("heading", { level: 1, name: "How Ghost checks its work" })).toBeVisible();

  const cta = page.locator("#eval-cta_calibration");
  await expect(cta.getByRole("rowheader")).toContainText("Is the ask the right size for where the buyer is?");
  // The drafted-action checks carry no quality column any more: judge quality is a System metric.
  await expect(cta.getByRole("cell")).toHaveText(["AI judge", "Yes", "Action/output generation"]);
  const system = page.locator("#job-system");
  await system.locator("summary").first().click();
  const quality = system.getByRole("region", { name: "Judge quality", exact: true });
  await expect(quality).toContainText("Recorded Oct 3, 2026");
  await expect(quality).toContainText("recorded measurement, not a live reading");
  await expect(quality).not.toContainText(/deepseek|qwen|gold|trials|bench/i);
  await expect(quality.locator("#judge-cta_calibration").getByRole("cell")).toHaveText(["99%", "0%", "26%", "84"]);
  await expect(page.locator("details.family")).toHaveCount(27);
  const e7 = page.locator("#family-E7");
  await e7.locator("summary").click();
  await expect(e7.getByRole("row").nth(1)).toBeVisible();
});

test("the web catalog leaves out the arm comparison and the out-of-scope evals", async ({ page }) => {
  await page.goto("/evals");
  const body = page.locator("main");
  await expect(body).not.toContainText(/B over A|C equals A|negative transfer/i);
  await expect(body).not.toContainText(/HAR-\d+/);
  const e16 = page.locator("#family-E16");
  await e16.locator("summary").click();
  await expect(e16).toContainText("Applicable learned knowledge shows up in later decisions");
  await expect(e16).not.toContainText("no change when knowledge is irrelevant");
  await expect(page.locator("#family-E22")).not.toContainText("irrelevant knowledge made to sound applicable");
});

test("a verdict's detail links to what that eval checks on the overview", async ({ page }) => {
  await page.goto(`/runs/${RUN}/evals`);
  const line = page.locator(`#result-${CTA_B}`);
  await line.locator("details.eval-more > summary").click();
  await line.getByRole("link", { name: "What this eval checks" }).click();
  await expect(page).toHaveURL(/\/evals#eval-cta_calibration$/);
  await expect(page.locator("#eval-cta_calibration")).toBeInViewport();
});
