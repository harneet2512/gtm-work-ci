// @vitest-environment jsdom
// The "what the edit changed" panel (HAR-145): struck before-spans, shipped replacements, and an honest note for edits it
// cannot place or must not draw (PR #66 LOW 12-13).
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { EditSpans } from "@/components/run/EditSpans";
import type { HumanStrategyDecision, RunStrategies } from "@/lib/api/types";

afterEach(cleanup);

const BODY = "Hello Fatoumata. Can we book a call? Thanks.";
const strategies = (body: string | null = BODY) =>
  ({ strategy_set: { candidates: [{ candidate_id: "c1", full_action_artifact: body === null ? null : { body } }] } }) as unknown as RunStrategies;
const dec = (edits: unknown[], finalBody: string | null = "Hello Fatoumata. Shall we talk Tuesday? Thanks.", selected = "c1") =>
  ({ selected_candidate_id: selected, final_artifact: finalBody === null ? null : { body: finalBody }, edits }) as unknown as HumanStrategyDecision;

describe("EditSpans", () => {
  it("renders nothing without edits, without a selected candidate, or without a draft body", () => {
    expect(render(<EditSpans decision={dec([])} strategies={strategies()} />).container.innerHTML).toBe("");
    cleanup();
    expect(render(<EditSpans decision={dec([{ kind: "x", before: "a", after: "b" }], null, "other")} strategies={strategies()} />).container.innerHTML).toBe("");
    cleanup();
    expect(render(<EditSpans decision={dec([{ kind: "x", before: "a", after: "b" }])} strategies={strategies(null)} />).container.innerHTML).toBe("");
    cleanup();
    expect(render(<EditSpans decision={dec([{ kind: "x", before: "a", after: "b" }])} strategies={null} />).container.innerHTML).toBe("");
  });

  it("strikes the invalidated text, shows the shipped replacement and names the kind", () => {
    const { container } = render(<EditSpans decision={dec([{ kind: "cta_changed", before: "Can we book a call?", after: "Shall we talk Tuesday?" }])} strategies={strategies()} />);
    expect(container.querySelector("del")!.textContent).toBe("Can we book a call?");
    expect(container.querySelector("ins")!.textContent).toBe("Shall we talk Tuesday?");
    expect(container.querySelector(".ekind")!.textContent).toBe("CTA changed");
    expect(container.textContent).toContain("Every located edit is present in the sent artifact.");
  });

  it("flags a replacement that is not in the sent artifact and uses the raw kind when it has no wording", () => {
    const { container } = render(<EditSpans decision={dec([{ kind: "tone_shift", before: "Thanks.", after: "Cheers." }], "Something else")} strategies={strategies()} />);
    expect(container.querySelector(".ekind")!.textContent).toBe("tone_shift — not in sent artifact");
    expect(container.textContent).toContain("A span not in the sent artifact is flagged.");
  });

  it("draws a before-only or after-only edit with just the side it recorded", () => {
    const beforeOnly = render(<EditSpans decision={dec([{ kind: "paragraph_edited", before: "Thanks." }])} strategies={strategies()} />).container;
    expect(beforeOnly.querySelector("del")).not.toBeNull();
    expect(beforeOnly.querySelector("ins")).toBeNull();
    expect(beforeOnly.querySelector(".ekind")!.textContent).toBe("paragraph edited");
    cleanup();
    const afterOnly = render(<EditSpans decision={dec([{ after: "New" }])} strategies={strategies()} />).container;
    expect(afterOnly.querySelector(".espan")).toBeNull();
  });

  it("lists edits whose before-text is not in the draft instead of painting them (singular and plural)", () => {
    const one = render(<EditSpans decision={dec([{ kind: "a", before: "not there", after: "x" }])} strategies={strategies()} />).container;
    expect(one.textContent).toContain("1 recorded edit could not be located in the draft text (a)");
    cleanup();
    const two = render(<EditSpans decision={dec([{ kind: "a", before: "nope", after: "x" }, { kind: "b", before: "nada", after: "y" }])} strategies={strategies()} />).container;
    expect(two.textContent).toContain("2 recorded edits could not be located");
  });

  it("does not draw an edit that overlaps an earlier one, and says so, so the draft text is never repeated", () => {
    const { container } = render(
      <EditSpans
        decision={dec([
          { kind: "first", before: "Can we book a call?", after: "A" },
          { kind: "second", before: "book a call? Thanks.", after: "B" },
        ])}
        strategies={strategies()}
      />,
    );
    expect(container.querySelectorAll("del")).toHaveLength(1);
    expect(container.textContent).toContain("1 edit overlaps an earlier edit's text (second) and is not drawn");
    cleanup();
    const plural = render(
      <EditSpans
        decision={dec([
          { kind: "first", before: "Hello Fatoumata. Can we", after: "A" },
          { kind: "second", before: "Fatoumata.", after: "B" },
          { kind: "third", before: "Can", after: "C" },
        ])}
        strategies={strategies()}
      />,
    ).container;
    expect(plural.textContent).toContain("2 edits overlap an earlier edit's text (second, third) and are not drawn");
  });
});
