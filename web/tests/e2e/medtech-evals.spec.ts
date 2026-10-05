import { expect, test } from "@playwright/test";

// The eval page on the real MedTech Advances demo case (CRMArena-Pro), end to end against the fixture core:
// who chose what, the trace strip into the account map, the evidence peek by keyboard, a blocking failure that
// locks Send, a dispute, and a phone-width layout that does not scroll sideways.

const RUN = "0f0aad00-0000-4000-8000-00000000ad60";
const ACCOUNT = "0a0cad00-0000-4000-8000-00000000ad01";
const EVENT = "05e0ad00-0000-4000-8000-00000000ad0d";
const C = "0ca0ad00-0000-4000-8000-00000000ada3";
const PAGE = `/runs/${RUN}/evals`;

test("the MedTech eval page leads with the judgment and says who chose what", async ({ page }) => {
  await page.goto(PAGE);
  await expect(page.getByRole("heading", { level: 1, name: "MedTech Advances: how Ghost judged this decision" })).toBeVisible();
  await expect(page.locator(".choice-row")).toContainText("Ghost's pick");
  await expect(page.locator(".choice-row")).toContainText("Book the call, bring the cost model");
  await expect(page.locator(".choice-row")).toContainText("Chosen by Luis Rodriguez");
  const banner = page.getByRole("region", { name: "Warn: Responds to the change" });
  await expect(banner).toContainText("says nothing about ongoing or integration costs");
  await expect(banner).toContainText("Fatoumata Touré · Nov 9, 2023 · Email");
});

test("the trace strip walks the decision and opens Event N on the account map", async ({ page }) => {
  await page.goto(PAGE);
  const trace = page.getByRole("navigation", { name: "Decision trace" });
  await expect(trace.getByRole("listitem")).toHaveCount(7);
  await expect(trace.locator("[aria-current='step']")).toContainText("14 verdicts");
  await expect(trace).toContainText("Luis Rodriguez chose B");
  await expect(trace).toContainText("Awaiting confirmation");
  await trace.getByRole("link", { name: /Email from Fatoumata Touré/ }).click();
  await expect(page).toHaveURL(new RegExp(`/accounts/${ACCOUNT}\\?event=${EVENT}&view=after$`));
  await expect(page.getByRole("heading", { level: 1, name: "MedTech Advances" })).toBeVisible();
  await expect(page.getByTestId("account-map").getByRole("button", { name: "Person: Fatoumata Touré" })).toBeVisible();
});

test("a verdict in the comparison shows its evidence on keyboard focus and opens its card", async ({ page }) => {
  await page.goto(PAGE);
  const table = page.getByRole("table", { name: /Every eval Ghost ran, by option/ });
  await expect(table.getByRole("rowheader").first()).toContainText("CTA calibration");
  const cell = table.locator("td.cell").nth(2).locator(".cell-link");
  await cell.focus();
  const peek = page.locator(`#${await cell.getAttribute("aria-describedby")}`);
  await expect(peek).toBeVisible();
  await expect(peek).toContainText("Could we schedule a follow-up call");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(new RegExp(`candidate=${C}#result-`));
  await expect(page.getByRole("region", { name: "Evals for option C: Match Quantum Circuits on onboarding" })).toBeVisible();
});

test("a blocking failure locks Send and says why", async ({ page }) => {
  await page.goto(`${PAGE}?candidate=${C}`);
  await expect(page.getByRole("region", { name: "Send is blocked: Pricing policy still fails." })).toBeVisible();
  const option = page.getByRole("region", { name: /Evals for option C/ });
  const gate = option.getByRole("note");
  await expect(gate).toContainText("If chosen, Send stays blocked: Pricing policy still fails.");
  await expect(gate).toContainText("CTA calibration");
  await expect(option.locator("li.eval-line").first()).toContainText("Blocks send");
  await expect(option.locator("li.eval-line").first().locator(".eval-line-meta")).toContainText("Rule check");
});

test("the chosen, edited email is honestly not re-evaluated yet", async ({ page }) => {
  await page.goto(PAGE);
  const after = page.getByRole("region", { name: "Not re-evaluated yet" });
  await expect(after).toContainText("Luis Rodriguez changed the call to action and edited a paragraph.");
  await expect(after).toContainText("Warn on CTA calibration");
});

test("This eval is wrong records a dispute on a MedTech verdict", async ({ page }) => {
  await page.goto(`${PAGE}?candidate=${C}`);
  const line = page.locator("li.eval-line").first();
  await line.getByRole("button", { name: "This eval is wrong" }).click();
  const form = line.getByRole("form", { name: "Dispute Pricing policy" });
  await form.getByLabel("What did the eval get wrong?").fill("Onboarding support is in the partner plan Luis can offer.");
  await form.getByRole("button", { name: "Send to eval review" }).click();
  await expect(line.getByRole("status")).toContainText("Disagreement recorded");
});

test("the three eval jobs stay separate: what Ghost understood, the decision, and a collapsed system panel", async ({ page }) => {
  await page.goto(PAGE);
  const job1 = page.getByRole("region", { name: "What Ghost understood from Event N" });
  await expect(job1).toContainText("moved the account state from v107 to v108");
  await expect(job1.locator(".understood")).toHaveCount(5);
  await expect(job1.getByRole("note")).toContainText("Verdicts on this understanding are not served yet.");
  await page.getByRole("navigation", { name: "Decision trace" }).getByRole("link", { name: /State change/ }).click();
  await expect(page).toHaveURL(/#intelligence-h$/);
  await expect(page.getByRole("region", { name: "Company knowledge in this decision" })).toContainText("No option cited company knowledge.");
  const system = page.locator("details.run-system");
  await expect(system.locator(".sys-checks")).toBeHidden();
  await system.locator("summary").click();
  await expect(system.locator(".sys-checks li")).toHaveCount(6);
});

test("the catalog files every eval under its job, system last and collapsed", async ({ page }) => {
  await page.goto("/evals");
  const jobs = page.getByRole("navigation", { name: "The three eval jobs" }).getByRole("link");
  await expect(jobs).toHaveCount(3);
  await expect(page.locator("#job-intelligence details.family")).toHaveCount(6);
  await expect(page.locator("#job-decision details.family")).toHaveCount(11);
  await expect(page.locator("#job-system")).not.toHaveAttribute("open", "");
});

test("Message 1's View evals target: the account map names the evals that judge its understanding", async ({ page }) => {
  await page.goto(`/accounts/${ACCOUNT}#intelligence-evals`);
  const panel = page.locator("#intelligence-evals");
  await expect(panel.getByRole("heading", { name: "How Ghost's understanding is checked" })).toBeVisible();
  await expect(panel).toContainText("Per-change verdicts are not served by the core yet.");
  await panel.getByRole("link", { name: "All intelligence-building evals" }).click();
  await expect(page).toHaveURL(/\/evals#job-intelligence$/);
});

test("at phone width the page does not scroll sideways; the comparison does", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(PAGE);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  const scroller = page.getByRole("region", { name: "Eval comparison, scrollable" });
  expect(await scroller.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
});
