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

// HAR-149 polish: the copy, the path order and the drawer's fit.
test("Why A won uses display labels and capital letters, and an edit appears as one card", async ({ page }) => {
  await page.goto(`/evals/episode/${MEDTECH}/decision`);
  const why = page.getByTestId("why-won");
  await expect(why).not.toContainText(/[a-z]+_[a-z]+/);
  await expect(why.locator(".dec-line").first()).toContainText(/Was the ranking right\? [A-Z]/);
  const entries = page.getByTestId("human-edit").locator(".dec-entry");
  const labels = await entries.locator(".dec-pair").allTextContents();
  expect(new Set(labels).size).toBe(labels.length);
});

test("in the path D6 comes right after D5 and before D7", async ({ page }) => {
  await page.goto(`/evals/episode/${MEDTECH}`);
  const gates = await page.locator(".path-node").evaluateAll((els) => els.map((e) => e.getAttribute("data-gate")));
  expect(gates.indexOf("D6")).toBe(gates.indexOf("D5") + 1);
  expect(gates.indexOf("D7")).toBe(gates.indexOf("D6") + 1);
  expect(gates.indexOf("D10")).toBe(gates.length - 1);
});

for (const [name, size] of [["1100", { width: 1100, height: 720 }], ["1200", { width: 1200, height: 720 }], ["1280", { width: 1280, height: 720 }], ["1440", { width: 1440, height: 800 }], ["phone", { width: 390, height: 844 }]] as const) {
  test(`the drawer fits and scrolls to its last section at ${name}`, async ({ page }) => {
    await page.setViewportSize(size);
    await page.goto(`/evals/episode/${MEDTECH}?gate=D3&demo=1`);
    const drawer = page.getByTestId("eval-drawer");
    await expect(drawer).toBeVisible();
    await page.waitForTimeout(400);
    const box = (await drawer.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(-1);
    expect(box.x + box.width).toBeLessThanOrEqual(size.width + 1);
    expect(box.y + box.height).toBeLessThanOrEqual(size.height + 1);
    const body = drawer.locator(".dr-body");
    const m = await body.evaluate((el) => ({ overflowY: getComputedStyle(el).overflowY, sideways: el.scrollWidth > el.clientWidth }));
    expect(m.overflowY).toBe("auto");
    expect(m.sideways).toBe(false);
    await body.evaluate((el) => el.scrollTo(0, el.scrollHeight));
    const lb = (await drawer.locator("[data-section='failures']").boundingBox())!;
    const bb = (await body.boundingBox())!;
    expect(lb.y + lb.height).toBeLessThanOrEqual(bb.y + bb.height + 1);
    // scrolled to the top the first section sits below the header, not under it
    await body.evaluate((el) => el.scrollTo(0, 0));
    const head = (await drawer.locator(".dr-head").boundingBox())!;
    const first = (await drawer.locator("[data-section='what']").boundingBox())!;
    expect(first.y).toBeGreaterThanOrEqual(head.y + head.height - 1);
    // on a laptop the path is not covered by the drawer; on a phone the drawer is the whole screen
    if (size.width >= 1100) {
      const node = (await page.locator(".path-node[data-gate='B1']").boundingBox())!;
      expect(node.x + node.width).toBeLessThanOrEqual(box.x + 1);
      const tabs = (await page.locator(".insp-nav a, .insp-nav [aria-current='page']").last().boundingBox())!;
      expect(tabs.x + tabs.width).toBeLessThanOrEqual(box.x + 1);
    }
    // the question is ink, not the muted heading colour
    expect(await drawer.locator(".dr-head h2").evaluate((el) => getComputedStyle(el).color)).not.toBe(await page.locator(".dr-meta").evaluate((el) => getComputedStyle(el).color));
    // the page itself does not scroll sideways
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
  });
}

test("Demo mode names no code location in the drawer; operator mode still does", async ({ page }) => {
  await page.goto(`/evals/episode/${MEDTECH}?gate=D8&demo=1`);
  const effect = page.getByTestId("eval-drawer").locator("[data-section='effect']");
  await expect(effect).toBeVisible();
  await expect(effect).not.toContainText("Where that is decided");
  await expect(effect).not.toContainText(/\.go|d8AsBlockingEvals|ScopeTooBroad/);
  await page.goto(`/evals/episode/${MEDTECH}?gate=D8`);
  await expect(page.getByTestId("eval-drawer").locator("[data-section='effect']")).toContainText("Where that is decided");
});

test("the rail column is painted down the whole page, not only one screen", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 700 });
  await page.goto(`/evals/episode/${MEDTECH}/decision?demo=1`);
  const m = await page.evaluate(() => {
    const shell = document.querySelector(".shell") as HTMLElement;
    return { tall: shell.scrollHeight > window.innerHeight * 2, bg: getComputedStyle(shell).backgroundImage };
  });
  expect(m.tall).toBe(true);
  expect(m.bg).toContain("linear-gradient");
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  const png = await page.screenshot({ clip: { x: 20, y: 650, width: 4, height: 4 } });
  expect(png.length).toBeGreaterThan(0);
});
