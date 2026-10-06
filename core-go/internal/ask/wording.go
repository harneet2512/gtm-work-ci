package ask

import (
	"regexp"
	"strings"
)

// Knowledge is described by what was retrieved, what applied and what was used, never by what it influenced: nothing
// in gtm_ai measures influence. The prompt says so, and this is the check that holds when the model does not listen.
const influenceRewrite = "Retrieved, applicable and used are different things; none of them measures influence."

var (
	// influenceWord matches influence, influences, influenced, influencing; "influencer" (a stakeholder role) is not it.
	influenceWord = regexp.MustCompile(`(?i)\binfluenc(?:e|es|ed|ing)\b`)
	// knowledgeTalk marks a sentence as being about knowledge, retrieval or guidance rather than about a person.
	knowledgeTalk = regexp.MustCompile(`(?i)knowledge|retriev|applicab|guidance|playbook|lesson|learn|\bused\b|\buse\b`)
	// negation marks a sentence that denies the claim, which is the honest wording and stays as written.
	negation    = regexp.MustCompile(`(?i)\b(?:not|no|never|none|cannot|can't|doesn't|does not|don't|do not|isn't|neither|nor|without)\b|n't\b`)
	sentenceEnd = regexp.MustCompile(`[^.!?\n]+[.!?]?(?:\s+|$)|\n+`)
)

// withoutInfluenceClaims rewrites every sentence that says knowledge influenced something; the rest is untouched.
// It returns the text and whether it changed anything.
func withoutInfluenceClaims(body string) (string, bool) {
	changed := false
	rewritten := false
	out := sentenceEnd.ReplaceAllStringFunc(body, func(sentence string) string {
		if !influenceWord.MatchString(sentence) || !knowledgeTalk.MatchString(sentence) || negation.MatchString(sentence) {
			return sentence
		}
		changed = true
		if rewritten { // say it once
			return ""
		}
		rewritten = true
		trail := sentence[len(strings.TrimRight(sentence, " \t")):]
		return influenceRewrite + trail
	})
	return strings.TrimSpace(out), changed
}
