package demorun

import (
	"context"
	"fmt"
	"strings"
)

// Checklist is what the user clicks in Slack after `demo play`, in order, then verifies.
func Checklist(channelID string) string {
	where := "#ghost-demo"
	if channelID != "" {
		where += " (" + channelID + ")"
	}
	return strings.Join([]string{
		"WHAT TO CLICK IN SLACK (" + where + ")",
		"  Message 1 is the account update. Message 2 is the strategy chooser.",
		"  1. In Message 2, click View full to read the three candidate strategies and their evals.",
		"  2. Click Choose on the candidate you want (gtm_ai's own preference is shown next to your choice).",
		"  3. Click Edit, change the recipients, subject or body, and save.",
		"  4. Click Send. This is a dry run: nothing is emailed; the final artifact is re-evaluated and recorded.",
		"  5. Message 3 appears with gtm_ai's inference of why you chose and edited as you did. Click Needs correction",
		"     and state the correction, and click Add note and write a note.",
		"  6. Run `demo verify`: it reads back the human decision, the HumanDelta, the judgment verdict and note",
		"     history and the Slack message count, with PASS/FAIL per step and the ids.",
	}, "\n")
}

// WebURLs lists the pages to open. A page that does not answer 2xx in this build is labelled, not offered.
func WebURLs(ctx context.Context, webBase, accountID, manifestID, runID string, exists func(context.Context, string) bool) []string {
	type page struct{ label, path string }
	pages := []page{
		{"account map", "/accounts/" + accountID},
		{"episode replay", "/replay"},
		{"replay of this case", "/replay/" + manifestID},
		{"runs", "/runs"},
	}
	if runID != "" {
		pages = append(pages, page{"run chain", "/runs/" + runID}, page{"run evals", "/runs/" + runID + "/evals"})
	}
	var out []string
	for _, p := range pages {
		u := webBase + p.path
		if exists(ctx, u) {
			out = append(out, fmt.Sprintf("  %-22s %s", p.label, u))
		} else {
			out = append(out, fmt.Sprintf("  %-22s (not in this build: %s)", p.label, p.path))
		}
	}
	return out
}
