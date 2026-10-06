package slacksurface

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

const (
	maxMissingShown    = 8
	missingDescription = 160
)

// transitionBlocks renders the relationship-state transition of Message 1: its status, from -> to and the facts it
// still lacks. A transition the event did not touch is the account's open one and is labelled unchanged; no
// transition at all is said plainly, so the message never leaves the reader guessing.
func transitionBlocks(t *Transition) []slack.Block {
	if t == nil {
		return []slack.Block{contextBlock("ghost.bi.transition", "_No open relationship transition._")}
	}
	to := "no clear target"
	if t.ToStateCandidate != nil {
		to = *t.ToStateCandidate
	}
	head := fmt.Sprintf("%s: %s → %s", mrkdwn(t.Status), mrkdwn(t.FromState), mrkdwn(to))
	if !t.TouchedByEvent {
		head += " _(unchanged by this event)_"
	}
	lines := make([]string, 0, len(t.MissingFacts))
	for _, m := range t.MissingFacts {
		need := "optional"
		if m.Required {
			need = "required"
		}
		line := fmt.Sprintf("%s (%s)", mrkdwn(humanize(strings.ReplaceAll(m.Key, "_", " "))), need)
		if d := strings.TrimSpace(m.Description); d != "" {
			line += ": " + mrkdwn(truncate(d, missingDescription))
		}
		lines = append(lines, line)
	}
	body := head
	if len(lines) > 0 {
		body += "\n*Still missing*\n" + bullets(lines, maxMissingShown)
	}
	return textSections("ghost.bi.transition", "Relationship transition", body, 2)
}
