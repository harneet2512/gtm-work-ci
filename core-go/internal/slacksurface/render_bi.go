package slacksurface

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// RenderBI renders Message 1, Cliff's intelligence message: "<ACCOUNT> changed", then What changed, Why it
// matters and Evidence (each numbered claim with its own quotes), and the account's relationship transition. The
// buttons are View account and View trace (the decision episode the control plane shows). Pure: the same inputs
// always yield the same blocks. accountURL and traceURL may be empty (no button then): the trace link only exists
// once the episode does.
func RenderBI(u BusinessIntelligenceUpdate, accountName, accountURL, traceURL string) Message {
	name := orDash(accountName)
	blocks := []slack.Block{headerBlock("ghost.bi.header", name+" changed")}
	if strings.TrimSpace(u.Summary) != "" {
		blocks = append(blocks, section("ghost.bi.summary", mrkdwn(truncate(u.Summary, 600))))
	}
	blocks = append(blocks, section("ghost.bi.changes", "*What changed*"))
	shown := min(len(u.Claims), maxChanges)
	for i, c := range u.Claims[:shown] {
		blocks = append(blocks, section(fmt.Sprintf("ghost.bi.claim.%d", i+1), fmt.Sprintf("*%d.* %s", i+1, mrkdwn(c.Statement))))
	}
	if len(u.Claims) > shown {
		blocks = append(blocks, contextBlock("ghost.bi.changes.more", fmt.Sprintf("_+%d %s_", len(u.Claims)-shown, moreInWeb)))
	}

	why := []string{mrkdwn(u.WhyItMatters)}
	if k := knowledgeLine(u.KnowledgeRefs); k != "" {
		why = append(why, "Relevant company knowledge: "+k)
	}
	blocks = append(blocks, textSections("ghost.bi.why", "Why it matters", bullets(why, maxChanges), 2)...)
	blocks = append(blocks, evidenceBlocks(u.Claims[:shown])...)
	blocks = append(blocks, transitionBlocks(u.Transition)...)

	var els []slack.BlockElement
	els = withLink(els, linkButton(ActionBIViewAccount, "View account", accountURL))
	els = withLink(els, linkButton(ActionBIViewTrace, "View trace", traceURL))
	if len(els) > 0 {
		blocks = append(blocks, actions("ghost.bi.actions", els...))
	}
	return Message{Text: fmt.Sprintf("%s changed: %s", name, truncate(u.Summary, 200)), Blocks: blocks}
}

// evidenceBlocks is the Evidence section: under the heading, each numbered claim's quotes, so every claim keeps
// its own trace.
func evidenceBlocks(claims []Claim) []slack.Block {
	blocks := []slack.Block{section("ghost.bi.evidence", "*Evidence*")}
	for i, c := range claims {
		ev := evidenceLines(c.EvidenceRefs)
		if len(ev) > maxClaimEvidence {
			ev = append(ev[:maxClaimEvidence], fmt.Sprintf("+%d %s", len(ev)-maxClaimEvidence, moreInWeb))
		}
		blocks = append(blocks, contextBlock(fmt.Sprintf("ghost.bi.evidence.%d", i+1), fmt.Sprintf("*%d.* %s", i+1, strings.Join(ev, "  |  "))))
	}
	return blocks
}
