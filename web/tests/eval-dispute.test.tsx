// @vitest-environment jsdom
// "This eval is wrong" (HAR-97 E19): the form, the server-side outcome mapping and the page loader.
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DisputeForm } from "@/components/evals/DisputeForm";
import { CoreError, InvalidIdError } from "@/lib/api/core-client";
import type { EvalDispute } from "@/lib/api/types";
import { actorLabelFromEnv, disputeMessage, recordDispute } from "@/lib/evals/dispute";
import { loadEvalPage } from "@/lib/load-eval-page";
import { loadExample } from "./contract-validator";

afterEach(cleanup);
const RESULT = "0e1a0000-0000-4000-8000-000000009201";

describe("DisputeForm", () => {
  it("opens on request, needs a reason, and sends the reason and the expected verdict", async () => {
    const user = userEvent.setup();
    const submit = vi.fn(async () => ({ ok: true as const, expected: "pass" as const }));
    render(<DisputeForm resultId={RESULT} evalName="CTA calibration" verdict="warn" submit={submit} />);

    const toggle = screen.getByRole("button", { name: "This eval is wrong" });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    await user.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    const form = screen.getByRole("form", { name: "Dispute CTA calibration" });
    expect(form).toBeTruthy();

    // The current verdict is not offered as the expected one.
    const options = screen.getAllByRole("option").map((o) => o.textContent);
    expect(options).toEqual(["Not sure", "Pass", "Fail", "Unsure", "Not relevant"]);

    await user.type(screen.getByLabelText("What did the eval get wrong?"), "   ");
    await user.click(screen.getByRole("button", { name: "Send to eval review" }));
    expect(screen.getByRole("status").textContent).toBe("Say what the eval got wrong before sending.");
    expect(submit).not.toHaveBeenCalled();

    await user.clear(screen.getByLabelText("What did the eval get wrong?"));
    await user.type(screen.getByLabelText("What did the eval get wrong?"), "Marco asked for the documents first.");
    await user.selectOptions(screen.getByLabelText("It should have been"), "pass");
    await user.click(screen.getByRole("button", { name: "Send to eval review" }));
    expect(submit).toHaveBeenCalledWith(RESULT, "Marco asked for the documents first.", "pass");
    expect((await screen.findByRole("status")).textContent).toMatch(/Disagreement recorded: you said it should be Pass/);
    expect(screen.queryByRole("form")).toBeNull();
  });

  it("shows the plain-language refusal and keeps the form, and Cancel closes it", async () => {
    const user = userEvent.setup();
    const submit = vi.fn(async () => ({ ok: false as const, message: "That is the verdict it already has." }));
    render(<DisputeForm resultId={RESULT} evalName="CTA calibration" verdict="warn" submit={submit} />);
    await user.click(screen.getByRole("button", { name: "This eval is wrong" }));
    await user.type(screen.getByLabelText("What did the eval get wrong?"), "x");
    await user.click(screen.getByRole("button", { name: "Send to eval review" }));
    expect((await screen.findByText("That is the verdict it already has.")).getAttribute("role")).toBe("status");
    expect(submit).toHaveBeenCalledWith(RESULT, "x", null);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("form")).toBeNull();
  });

  it("acknowledges a reasoning-only dispute without naming a verdict", async () => {
    const user = userEvent.setup();
    render(<DisputeForm resultId={RESULT} evalName="Grounding" verdict="pass" submit={async () => ({ ok: true, expected: null })} />);
    await user.click(screen.getByRole("button", { name: "This eval is wrong" }));
    await user.type(screen.getByLabelText("What did the eval get wrong?"), "Wrong call quoted.");
    await user.click(screen.getByRole("button", { name: "Send to eval review" }));
    expect((await screen.findByRole("status")).textContent).toMatch(/^Disagreement recorded\. It goes to the eval review/);
  });
});

describe("recordDispute", () => {
  const dispute = loadExample<EvalDispute>("eval_dispute");

  it("posts surface web, the configured actor and a trimmed reason", async () => {
    const api = { disputeEvalResult: vi.fn(async () => dispute) };
    await expect(recordDispute(api, { resultId: RESULT, reason: "  Should warn.  ", expected: "warn" }, "Dana Kim")).resolves.toEqual({ ok: true, expected: "warn" });
    expect(api.disputeEvalResult).toHaveBeenCalledWith(RESULT, { reason: "Should warn.", expected_verdict: "warn", surface: "web", actor_label: "Dana Kim" });
  });

  it("refuses an empty reason or an unknown verdict before calling the core", async () => {
    const api = { disputeEvalResult: vi.fn(async () => dispute) };
    expect(await recordDispute(api, { resultId: RESULT, reason: " ", expected: null }, "a")).toEqual({ ok: false, message: disputeMessage("invalid_request") });
    expect(await recordDispute(api, { resultId: RESULT, reason: "é".repeat(2001), expected: null }, "a")).toMatchObject({ ok: false });
    expect(await recordDispute(api, { resultId: RESULT, reason: "x", expected: "maybe" }, "a")).toMatchObject({ ok: false });
    expect(api.disputeEvalResult).not.toHaveBeenCalled();
  });

  it("maps the core's refusals to messages that say what to do", async () => {
    const failing = (e: unknown) => ({ disputeEvalResult: vi.fn(async () => Promise.reject(e)) });
    expect(await recordDispute(failing(new CoreError(422, "expected_equals_verdict", "x")), { resultId: RESULT, reason: "x", expected: "warn" }, "a")).toEqual({
      ok: false,
      message: "That is the verdict it already has. Pick a different one, or choose “Not sure”.",
    });
    expect(await recordDispute(failing(new CoreError(0, "unreachable", "x")), { resultId: RESULT, reason: "x", expected: null }, "a")).toMatchObject({ message: /can't be reached/ });
    expect(await recordDispute(failing(new InvalidIdError("..")), { resultId: "..", reason: "x", expected: null }, "a")).toMatchObject({ message: /no longer in the core/ });
    expect(await recordDispute(failing(new Error("boom")), { resultId: RESULT, reason: "x", expected: null }, "a")).toMatchObject({ message: "The dispute could not be saved (unexpected). Try again." });
  });

  it("labels the web actor from the environment, with a plain default", () => {
    expect(actorLabelFromEnv({ GHOST_WEB_ACTOR_LABEL: " Dana Kim " })).toBe("Dana Kim");
    expect(actorLabelFromEnv({})).toBe("Web reviewer");
  });
});

describe("loadEvalPage", () => {
  it("reads the run chain and is honest that no send-time re-evaluation is served yet", async () => {
    const run = loadExample<{ id: string }>("agent_run");
    const api = {
      getRun: vi.fn(async () => run),
      getRunTrace: vi.fn(async () => null),
      getRunStrategies: vi.fn(async () => null),
      getStrategyDecision: vi.fn(async () => null),
      getJudgmentInference: vi.fn(async () => null),
      getKnowledge: vi.fn(),
    };
    const data = await loadEvalPage(api as never, run.id);
    expect(data.run).toBe(run);
    expect(data.reevaluation).toBeNull();
    expect(data.notices).toEqual([]);
  });
});
