package slacksurface

import (
	"net/url"
	"strings"

	"github.com/slack-go/slack"
)

// Links from Slack into the web (contracts/slack/actions.md, View evals). Every builder returns "" unless the web
// base is an absolute http(s) URL, so a message without GHOST_WEB_URL simply has no link buttons.

func webBase(web string) string { return safeURL(strings.TrimRight(web, "/")) }

// AccountURL is the web account page (the graph, state and timeline): Message 1's View account.
func AccountURL(web, accountID string) string {
	base := webBase(web)
	if base == "" || accountID == "" {
		return ""
	}
	return base + "/accounts/" + url.PathEscape(accountID)
}

// TraceURL is the control plane's page for one decision episode (its trace, with the inspector): Message 1's
// View trace. It is "" until the episode is known. The page resolves Message 1 only through the replay manifest,
// so the link carries ?manifest= whenever the manifest is known.
func TraceURL(web, episodeID, manifestID string) string {
	base := webBase(web)
	if base == "" || episodeID == "" {
		return ""
	}
	u := base + "/episodes/" + url.PathEscape(episodeID)
	if manifestID != "" {
		u += "?manifest=" + url.QueryEscape(manifestID)
	}
	return u
}

// EvalsURL is a run's eval page (job 2): the three options compared, or one option's evals when candidateID is set.
func EvalsURL(web, runID, candidateID string) string {
	base := webBase(web)
	if base == "" || runID == "" {
		return ""
	}
	u := base + "/runs/" + url.PathEscape(runID) + "/evals"
	if candidateID != "" {
		u += "?candidate=" + url.QueryEscape(candidateID)
	}
	return u
}

// EvalResultURL deep-links one verdict on the run's eval page, where its evidence opens.
func EvalResultURL(web, runID, resultID string) string {
	page := EvalsURL(web, runID, "")
	if page == "" || resultID == "" {
		return ""
	}
	return page + "#result-" + url.PathEscape(resultID)
}

// linkButton is a URL button; nil when there is no link, so callers can skip it.
func linkButton(actionID, label, link string) *slack.ButtonBlockElement {
	if safeURL(link) == "" {
		return nil
	}
	b := slack.NewButtonBlockElement(actionID, "", plain(label))
	b.URL = link
	return b
}

// withLink appends a link button to an element list when there is one.
func withLink(els []slack.BlockElement, b *slack.ButtonBlockElement) []slack.BlockElement {
	if b == nil {
		return els
	}
	return append(els, b)
}

// isLinkAction reports URL buttons: Slack opens their link itself and still posts an interaction, which is
// acknowledged and otherwise ignored.
func isLinkAction(actionID string) bool {
	switch actionID {
	case ActionBIViewAccount, ActionBIViewTrace, ActionStrategyViewEvals, ActionSelectedViewEvals, ActionJudgmentViewEvals:
		return true
	}
	return false
}
