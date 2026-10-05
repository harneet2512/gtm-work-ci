package slacksurface

import (
	"strings"
	"testing"
)

// Slack says what the web says (contracts/slack/eval-copy.md): plain eval names, the verdict emoji and label,
// worst first, the worst non-pass eval with its reason on the card, the blocking phrase, an Evidence link per
// line, tags, the not-relevant line, and "Not re-evaluated yet" under an edited draft's verdicts.
func TestCardSummaryUsesTheSharedWording(t *testing.T) {
	f := NewFixture()
	a, _ := f.Strategies.Candidate(fixtureCandA)
	got := evalSummary(f.Strategies.Bundle(a))
	// Worst first, as on the web: failures before passes.
	if !strings.HasPrefix(got, ":x: 1 fail · :warning: 1 warn: ") {
		t.Fatalf("card summary must count worst first with the verdict emoji: %s", got)
	}
	if strings.Contains(got, "Cta calibration") || strings.Contains(got, "PASS") || strings.Contains(got, "cta_calibration") {
		t.Fatalf("card summary must use plain names and labels, never codes: %s", got)
	}
	if !strings.Contains(got, "CTA calibration.") {
		t.Fatalf("card summary must name the worst eval with its reason: %s", got)
	}
	all := EvalBundle{Items: []EvalItem{{EvalType: "grounding", Verdict: VerdictPass}, {EvalType: "timing_cadence", Verdict: VerdictPass}}}
	if got := evalSummary(all); got != ":white_check_mark: 2 pass" {
		t.Fatalf("all pass: %s", got)
	}
	if got := evalSummary(EvalBundle{}); got != "No relevant evals" {
		t.Fatalf("empty: %s", got)
	}
}

func TestSelectedEvalsAreWorstFirstWithEvidenceTagsAndNotRelevant(t *testing.T) {
	f := NewFixture()
	b, _ := f.Strategies.Candidate(fixtureCandB)
	bundle := f.Strategies.Bundle(b)
	blocks := evalSections("ghost.selected.evals", bundle, FixtureRunID, testWeb, true, maxEvalSections)
	raw := mustJSON(Message{Blocks: blocks})
	warn := strings.Index(raw, ":warning: *CTA calibration*  Warn")
	pass := strings.Index(raw, ":white_check_mark: *Relationship continuity*  Pass")
	if warn < 0 || pass < 0 || warn > pass {
		t.Fatalf("lines must be worst first in the shared wording: %s", raw)
	}
	// encoding/json HTML-escapes the link's closing bracket in marshaled output, so assert its escaped form.
	if !strings.Contains(raw, "/evals#result-") || !strings.Contains(raw, "|Evidence\\u003e") {
		t.Fatalf("each line links to its evidence on the web eval page: %s", raw)
	}
	for _, want := range []string{":heavy_minus_sign: Not relevant here (1): Economic buyer", "Sales methodology", "Not re-evaluated yet"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("missing %q: %s", want, raw)
		}
	}
}

func TestBlockingFailureCarriesTheBlockingPhrase(t *testing.T) {
	bundle := EvalBundle{Items: []EvalItem{{EvalType: "pricing_integrity", Verdict: VerdictFail, Result: &EvalResult{ID: "r1", Blocking: true, Reason: "The discount is not in the quote."}}}}
	raw := mustJSON(Message{Blocks: evalSections("x", bundle, FixtureRunID, "", false, maxEvalSections)})
	if !strings.Contains(raw, ":x: *Pricing policy*  Fail · Blocks send  The discount is not in the quote.") {
		t.Fatalf("blocking line: %s", raw)
	}
	if strings.Contains(raw, "Not re-evaluated") || strings.Contains(raw, "Evidence") {
		t.Fatalf("unedited and no web URL: no re-evaluation note and no links: %s", raw)
	}
}

func TestJudgmentEvalDifferencesUseTheSharedWording(t *testing.T) {
	f := NewFixture()
	raw := mustJSON(RenderJudgment(f.Inference, f.Strategies, FixtureRunID, ""))
	if strings.Contains(raw, "Cta calibration") || strings.Contains(raw, "FAIL vs WARN") {
		t.Fatalf("Message 3 must name evals and verdicts in the shared wording: %s", raw)
	}
	if !strings.Contains(raw, "CTA calibration: Ghost's pick :x: Fail, your choice :warning: Warn") {
		t.Fatalf("eval difference line: %s", raw)
	}
}
