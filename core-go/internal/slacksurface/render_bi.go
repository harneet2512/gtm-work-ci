package slacksurface

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// RenderBI renders Message 1: what changed, why it matters, evidence per claim, then View account map and View
// evals (the account's intelligence-building evals, eval job 1). Pure: the same inputs always yield the same
// blocks. mapURL and evalsURL may be empty (no button then).
func RenderBI(u BusinessIntelligenceUpdate, accountName, mapURL, evalsURL string) Message {
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

	var els []slack.BlockElement
	if link := safeURL(mapURL); link != "" {
		btn := slack.NewButtonBlockElement(ActionViewMap, u.ID, plain("View account map"))
		btn.URL = link
		els = append(els, btn)
	}
	els = withLink(els, linkButton(ActionBIViewEvals, "View evals", evalsURL))
	if len(els) > 0 {
		blocks = append(blocks, actions("ghost.bi.actions", els...))
	}
	return Message{Text: fmt.Sprintf("%s changed: %s", name, truncate(u.Summary, 200)), Blocks: blocks}
}
