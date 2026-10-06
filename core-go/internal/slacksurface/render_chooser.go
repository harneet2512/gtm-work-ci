package slacksurface

import (
	"fmt"

	"github.com/slack-go/slack"
)

// Compact card caps: the complete draft is one click away (Select, View full), so the card only has to let the
// human compare the three paths.
const (
	cardPreviewChars   = 220
	cardRationaleChars = 280
)

// RenderChooser renders Message 2 before a choice, in Cliff's voice: "I see 3 reasonable paths.", then the three
// already-generated options as compact cards A, B and C with the recommended one marked, then View evals to the web
// comparison of all three (when web is configured). Selecting an option rewrites this same message into the
// expanded action (RenderSelected). Nothing here triggers generation; every candidate is complete in the StrategySet.
func RenderChooser(rs RunStrategies, web string) Message {
	set := rs.StrategySet
	opening := fmt.Sprintf("I see %d reasonable paths.", len(set.Candidates))
	recommended := "-"
	for i, c := range set.Candidates {
		if c.PreferredByAgent {
			recommended = letter(i) + ". " + mrkdwn(c.Title)
		}
	}
	prefLine := "I recommend *" + recommended + "*"
	if set.NoAcceptableCandidate {
		prefLine = "I recommend none of these: every option is blocked or restricted. The choice is yours."
	}
	blocks := []slack.Block{
		headerBlock("ghost.strategy.header", opening),
		contextBlock("ghost.strategy.pref", prefLine),
	}
	for i, c := range set.Candidates {
		blocks = append(blocks, candidateCard(set, rs.Bundle(c), i, c)...)
	}
	if b := linkButton(ActionStrategyViewEvals, "View evals", EvalsURL(web, set.AgentRunID, "")); b != nil {
		blocks = append(blocks, slack.NewDividerBlock(), actions("ghost.strategy.links", b))
	}
	return Message{Text: opening, Blocks: blocks}
}

func candidateCard(set StrategySet, bundle EvalBundle, i int, c StrategyCandidate) []slack.Block {
	id := "ghost.strategy.card." + c.CandidateID
	recommended := c.PreferredByAgent && !set.NoAcceptableCandidate
	title := fmt.Sprintf("*%s. %s*", letter(i), mrkdwn(c.Title))
	if recommended {
		title += "  :star: _Recommended_"
	}
	t := Target{RunID: set.AgentRunID, CandidateID: c.CandidateID}
	selectBtn := button(ActionStrategyChoose, "Select "+letter(i), t)
	if recommended {
		selectBtn = selectBtn.WithStyle(slack.StylePrimary)
	}
	return []slack.Block{
		slack.NewDividerBlock(),
		section(id, title+"\n"+mrkdwn(truncate(c.Description, 300))),
		section(id+".preview", "> "+quote(mrkdwn(truncate(previewOf(c), cardPreviewChars)))),
		section(id+".why", "*Why*\n"+mrkdwn(truncate(c.Rationale, cardRationaleChars))),
		contextBlock(id+".evals", "*Evals*  "+conciseEvals(bundle, false)),
		actions("ghost.strategy.actions."+c.CandidateID, button(ActionStrategyViewFull, "View full", t), selectBtn),
	}
}

// previewOf prefers the contract's preview and falls back to the start of the body.
func previewOf(c StrategyCandidate) string {
	if c.Preview != "" {
		return c.Preview
	}
	return c.FullActionArtifact.Body
}

// quote prefixes continuation lines so a multi-line preview stays inside one mrkdwn quote.
func quote(s string) string {
	out := make([]byte, 0, len(s)+8)
	for i := 0; i < len(s); i++ {
		out = append(out, s[i])
		if s[i] == '\n' {
			out = append(out, '>', ' ')
		}
	}
	return string(out)
}
