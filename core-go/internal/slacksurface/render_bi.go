package slacksurface

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/slack-go/slack"
)

// MapURL is the web account-map link for an update: the account graph at the update's state with the
// change highlighted. Empty when no web base URL is configured.
func MapURL(webBase string, u BusinessIntelligenceUpdate) string {
	base := safeURL(strings.TrimRight(webBase, "/"))
	if base == "" {
		return ""
	}
	q := url.Values{"change": {u.AccountChangeID}}
	return base + "/accounts/" + url.PathEscape(u.AccountID) + "/map?" + q.Encode()
}

// RenderBI renders Message 1: what changed, why it matters, evidence per claim, View account map.
// Pure: the same inputs always yield the same blocks. mapURL may be empty (no button then).
func RenderBI(u BusinessIntelligenceUpdate, accountName, mapURL string) Message {
	name := orDash(accountName)
	blocks := []slack.Block{headerBlock("ghost.bi.header", name+" changed")}
	if strings.TrimSpace(u.Summary) != "" {
		blocks = append(blocks, section("ghost.bi.summary", mrkdwn(truncate(u.Summary, 600))))
	}
	blocks = append(blocks, section("ghost.bi.changes", "*What changed*"))
	for i, c := range u.Claims {
		if i == maxChanges {
			blocks = append(blocks, contextBlock("ghost.bi.changes.more", fmt.Sprintf("_+%d %s_", len(u.Claims)-maxChanges, moreInWeb)))
			break
		}
		// Each claim is numbered and its own evidence sits directly under it.
		blocks = append(blocks, section(fmt.Sprintf("ghost.bi.claim.%d", i+1), fmt.Sprintf("*%d.* %s", i+1, mrkdwn(c.Statement))))
		ev := evidenceLines(c.EvidenceRefs)
		if len(ev) > maxClaimEvidence {
			ev = append(ev[:maxClaimEvidence], fmt.Sprintf("+%d %s", len(ev)-maxClaimEvidence, moreInWeb))
		}
		blocks = append(blocks, contextBlock(fmt.Sprintf("ghost.bi.evidence.%d", i+1), "Evidence: "+strings.Join(ev, "  |  ")))
	}

	why := []string{mrkdwn(u.WhyItMatters)}
	if k := knowledgeLine(u.KnowledgeRefs); k != "" {
		why = append(why, "Relevant company knowledge: "+k)
	}
	blocks = append(blocks, textSections("ghost.bi.why", "Why this matters", bullets(why, maxChanges), 2)...)
	blocks = append(blocks, transitionBlocks(u.Transition)...)

	if link := safeURL(mapURL); link != "" {
		btn := slack.NewButtonBlockElement(ActionViewMap, u.ID, plain("View account map"))
		btn.URL = link
		blocks = append(blocks, actions("ghost.bi.actions", btn))
	}
	return Message{Text: fmt.Sprintf("%s changed: %s", name, truncate(u.Summary, 200)), Blocks: blocks}
}
