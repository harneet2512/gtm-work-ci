package slacksurface

import (
	"fmt"

	"github.com/slack-go/slack"
)

// RenderChooser renders Message 2 before a choice: all three complete candidates in one message.
// Nothing here triggers generation; every candidate is already complete in the StrategySet.
func RenderChooser(rs RunStrategies, dir Directory) Message {
	set := rs.StrategySet
	pref := "-"
	for i, c := range set.Candidates {
		if c.PreferredByAgent {
			pref = letter(i) + ". " + mrkdwn(c.Title)
		}
	}
	prefLine := "Ghost currently prefers: *" + pref + "*"
	if set.NoAcceptableCandidate {
		prefLine = "Ghost recommends none of these: every candidate is blocked or restricted. The choice is yours."
	}
	blocks := []slack.Block{
		headerBlock("ghost.strategy.header", "Choose the next move"),
		contextBlock("ghost.strategy.pref", prefLine),
	}
	for i, c := range set.Candidates {
		blocks = append(blocks, candidateCard(set, rs.Bundle(c), dir, i, c)...)
	}
	return Message{Text: fmt.Sprintf("Choose the next move (%d candidates)", len(set.Candidates)), Blocks: blocks}
}

func candidateCard(set StrategySet, bundle EvalBundle, dir Directory, i int, c StrategyCandidate) []slack.Block {
	id := "ghost.strategy.card." + c.CandidateID
	title := fmt.Sprintf("*%s. %s*", letter(i), mrkdwn(c.Title))
	if c.PreferredByAgent && !set.NoAcceptableCandidate {
		title += "  _(Ghost's pick)_"
	}
	t := Target{RunID: set.AgentRunID, CandidateID: c.CandidateID}
	view := viewOf(c, nil, dir)
	return []slack.Block{
		slack.NewDividerBlock(),
		section(id, title+"\n"+mrkdwn(truncate(c.Description, 300))),
		artifactFields(id+".addr", view),
		section(id+".preview", "> "+quote(mrkdwn(truncate(previewOf(c), previewChars)))),
		section(id+".why", "*Why this strategy*\n"+mrkdwn(truncate(c.Rationale, rationaleChars))),
		contextBlock(id+".evals", "*Evals:* "+evalSummary(bundle)),
		actions("ghost.strategy.actions."+c.CandidateID,
			button(ActionStrategyViewFull, "View full", t),
			button(ActionStrategyChoose, "Choose", t).WithStyle(slack.StylePrimary),
		),
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
