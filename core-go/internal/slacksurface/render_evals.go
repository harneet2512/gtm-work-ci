package slacksurface

import (
	"fmt"
	"sort"
	"strings"

	"github.com/slack-go/slack"
)

// Evals in Slack, in the shared wording (contracts/slack/eval-copy.md): the same names, labels, emoji and order
// as the web, worst first, never a score. Level 1 is the card's one line; Level 2 the selected action's lines.

// relevant drops the evals judged not relevant to this candidate.
func relevant(b EvalBundle) []EvalItem {
	var out []EvalItem
	for _, it := range b.Items {
		if it.Verdict != VerdictNotRelevant {
			out = append(out, it)
		}
	}
	return out
}

func isBlocking(it EvalItem) bool {
	return it.Result != nil && it.Result.Blocking && it.Verdict == VerdictFail
}

// worstFirst orders fail, warn, unsure, pass; a blocking failure before any other; otherwise the router's order.
func worstFirst(items []EvalItem) []EvalItem {
	out := append([]EvalItem(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		if oi, oj := verdictOrder(out[i].Verdict), verdictOrder(out[j].Verdict); oi != oj {
			return oi < oj
		}
		return isBlocking(out[i]) && !isBlocking(out[j])
	})
	return out
}

func reasonOf(it EvalItem) string {
	if it.Result == nil {
		return ""
	}
	return it.Result.Reason
}

// evalSummary is the card's line (Level 1): the counts worst first, then the worst non-pass eval with the first
// sentence of its reason, e.g. ":x: 2 fail · :white_check_mark: 1 pass: CTA calibration. Marco said …".
func evalSummary(b EvalBundle) string {
	items := worstFirst(relevant(b))
	if len(items) == 0 {
		return "No relevant evals"
	}
	counts := map[Verdict]int{}
	for _, it := range items {
		counts[it.Verdict]++
	}
	var parts []string
	for _, v := range []Verdict{VerdictFail, VerdictWarn, VerdictAbstain, VerdictPass} {
		if n := counts[v]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d %s", verdictEmoji(v), n, strings.ToLower(verdictLabel(v))))
		}
	}
	s := strings.Join(parts, " · ")
	if worst := items[0]; worst.Verdict != VerdictPass {
		s += ": " + mrkdwn(evalName(worst.EvalType)) + ". " + mrkdwn(firstSentence(reasonOf(worst)))
	}
	return s
}

// conciseEvals is the eval summary of a candidate card and of the selected action: the Level 1 line, the blocking
// phrase when a blocking eval fails, and, under an edited draft, that the verdicts were not re-evaluated. The
// per-eval depth (reasons, evidence, every verdict) is the web eval page, one Inspect evals click away.
func conciseEvals(b EvalBundle, edited bool) string {
	s := evalSummary(b)
	for _, it := range relevant(b) {
		if isBlocking(it) {
			s += " · " + phrase("blocking", nil)
			break
		}
	}
	if edited {
		s += "\n" + verdictEmoji("not_checked") + " " + phrase("not_reevaluated", nil) + ": these verdicts are on my draft, not on the edited version."
	}
	return s
}

// evalLine is one eval of the selected action (Level 2): emoji, name, label, the blocking phrase, the reason and,
// with a web URL, an Evidence link to that verdict on the run's eval page.
func evalLine(it EvalItem, runID, web string) string {
	line := verdictEmoji(it.Verdict) + " *" + mrkdwn(evalName(it.EvalType)) + "*  " + verdictLabel(it.Verdict)
	if isBlocking(it) {
		line += " · " + phrase("blocking", nil)
	}
	if r := reasonOf(it); r != "" {
		line += "  " + mrkdwn(truncate(r, reasonChars))
	}
	if it.Result != nil {
		if link := EvalResultURL(web, runID, it.Result.ID); link != "" {
			line += "  <" + link + "|" + phrase("evidence", nil) + ">"
		}
	}
	return line
}

// evalNotes is the context under the lines: the not-relevant evals collapsed into one line, the evidence-class tags
// of the lines above, and, under an edited draft, that these verdicts were not re-evaluated.
func evalNotes(b EvalBundle, lines []EvalItem, edited bool) []string {
	var notes []string
	var names []string
	for _, it := range b.Items {
		if it.Verdict == VerdictNotRelevant {
			names = append(names, mrkdwn(evalName(it.EvalType)))
		}
	}
	if len(names) > 0 {
		summary := phrase("not_relevant_summary", map[string]string{"count": fmt.Sprint(len(names))})
		notes = append(notes, verdictEmoji(VerdictNotRelevant)+" "+summary+": "+strings.Join(names, ", "))
	}
	var tags []string
	for _, it := range lines {
		if it.Result == nil {
			continue
		}
		if tag := evidenceClassTag(it.Result.EvidenceClass); tag != "" && !hasString(tags, tag) {
			tags = append(tags, tag)
		}
	}
	if len(tags) > 0 {
		notes = append(notes, strings.Join(tags, " · "))
	}
	if edited {
		notes = append(notes, verdictEmoji("not_checked")+" "+phrase("not_reevaluated", nil)+": these verdicts are on Ghost's draft, not on the edited version.")
	}
	return notes
}

func hasString(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// evalSections renders the selected action's evals (Level 2) worst first, then the notes as one context block.
func evalSections(blockID string, b EvalBundle, runID, web string, edited bool, max int) []slack.Block {
	items := worstFirst(relevant(b))
	if len(items) == 0 {
		return []slack.Block{section(blockID, "*Evals for this action*\nNo relevant evals")}
	}
	lines := make([]string, 0, len(items))
	for _, it := range items {
		lines = append(lines, evalLine(it, runID, web))
	}
	blocks := textSections(blockID, "Evals for this action", strings.Join(lines, "\n"), max)
	if notes := evalNotes(b, items, edited); len(notes) > 0 {
		blocks = append(blocks, contextBlock(blockID+".notes", strings.Join(notes, "\n")))
	}
	return blocks
}
