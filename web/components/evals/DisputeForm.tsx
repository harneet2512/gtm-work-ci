"use client";

import { useId, useState, useTransition, type FormEvent } from "react";
import type { DisputeOutcome, ExpectedVerdict } from "@/lib/evals/dispute";
import { EXPECTED_VERDICTS } from "@/lib/evals/dispute";
import { WORDING, verdictWording, type BundleVerdict } from "@/lib/evals/vocabulary";

export type SubmitDispute = (resultId: string, reason: string, expected: string | null) => Promise<DisputeOutcome>;

const MAX_REASON = 2000;

/**
 * "This eval is wrong" on one verdict (spec: disagreement is supervision; it feeds eval-of-evals, HAR-97 E19).
 * A closed button until asked; then a reason (required) and, optionally, the verdict it should have been. The
 * outcome is announced politely; a saved dispute replaces the form so it is not sent twice.
 */
export function DisputeForm({ resultId, evalName, verdict, submit }: { resultId: string; evalName: string; verdict: BundleVerdict; submit: SubmitDispute }) {
  const id = useId();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [expected, setExpected] = useState("");
  const [message, setMessage] = useState<string | null>(null);
  const [saved, setSaved] = useState<ExpectedVerdict | null | undefined>(undefined);
  const [pending, startTransition] = useTransition();

  if (saved !== undefined) {
    return (
      <p className="dispute-saved" role="status">
        Disagreement recorded{saved ? `: you said it should be ${verdictWording(saved).label}` : ""}. It goes to the eval review, where disagreements are how Ghost's evals get checked.
      </p>
    );
  }

  const onSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (reason.trim() === "") {
      setMessage("Say what the eval got wrong before sending.");
      return;
    }
    setMessage(null);
    startTransition(async () => {
      const outcome = await submit(resultId, reason, expected === "" ? null : expected);
      if (outcome.ok) setSaved(outcome.expected);
      else setMessage(outcome.message);
    });
  };

  return (
    <div className="dispute">
      <button type="button" className="link-button" aria-expanded={open} aria-controls={`${id}-form`} onClick={() => setOpen(!open)}>
        {WORDING.phrases.dispute}
      </button>
      {open ? (
        <form id={`${id}-form`} className="dispute-form" onSubmit={onSubmit} aria-label={`Dispute ${evalName}`}>
          <label htmlFor={`${id}-reason`}>What did the eval get wrong?</label>
          <textarea
            id={`${id}-reason`}
            name="reason"
            rows={3}
            maxLength={MAX_REASON}
            required
            autoComplete="off"
            placeholder="For example: Marco asked for the documents first, so the call offer is fine…"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
          <label htmlFor={`${id}-expected`}>It should have been</label>
          <select id={`${id}-expected`} name="expected" value={expected} onChange={(e) => setExpected(e.target.value)}>
            <option value="">Not sure</option>
            {EXPECTED_VERDICTS.filter((v) => v !== verdict).map((v) => (
              <option key={v} value={v}>
                {verdictWording(v).label}
              </option>
            ))}
          </select>
          <div className="dispute-actions">
            <button type="submit" disabled={pending}>
              {pending ? "Sending…" : "Send to eval review"}
            </button>
            <button type="button" className="secondary" onClick={() => setOpen(false)}>
              Cancel
            </button>
          </div>
          <p className="dispute-message" role="status" aria-live="polite">
            {message}
          </p>
        </form>
      ) : null}
    </div>
  );
}
