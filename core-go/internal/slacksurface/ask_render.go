package slacksurface

import (
	"regexp"
	"strings"

	"github.com/slack-go/slack"
)

// Ask Cliff copy. Cliff speaks; no internal id, field name or product name other than gtm_ai reaches a person.
const (
	askThinking     = "Thinking…"
	askHelp         = "Ask me about an account, what changed, how an option scored, or say \"play the next event\"."
	askBusy         = "I am still working on your last question. Ask again in a moment."
	askFailed       = "I could not answer that just now. Try again in a moment."
	askUnavailable  = "Ask Cliff is not available here right now."
	askProviderDown = "I cannot reach my model right now, so I cannot answer. Try again later."
	askCancelled    = "Cancelled. Nothing was changed."
	askRunning      = "Working on it…"
	askActionFailed = "That did not go through. Nothing was changed that I can confirm; check the control plane before trying again."
	askNotAllowed   = "Ask Cliff is limited to the demo team for now, so I cannot take this one. Ask a presenter to run it for you."

	askSectionLimit = 2900 // Slack's section text limit is 3000
	askMaxBlocks    = 20
)

// Action ids of the confirmation buttons (internal protocol ids, never shown).
const (
	ActionAskRun    = "ghost.ask.run"
	ActionAskCancel = "ghost.ask.cancel"
	askBlockID      = "ghost.ask.confirm"
)

var (
	mdLink = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)\s]+)\)`)
	mdBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)
)

// toMrkdwn converts the core's Markdown to Slack mrkdwn: **bold** to *bold*, [label](url) to <url|label>, and
// escapes what Slack treats as control characters.
func toMrkdwn(md string) string {
	var links []string
	masked := mdLink.ReplaceAllStringFunc(md, func(m string) string {
		sub := mdLink.FindStringSubmatch(m)
		links = append(links, "<"+sub[2]+"|"+escapeMrkdwn(sub[1])+">")
		return "\x00" + string(rune('A'+len(links)-1)) + "\x00"
	})
	out := escapeMrkdwn(masked)
	out = mdBold.ReplaceAllString(out, "*$1*")
	for i, l := range links {
		out = strings.Replace(out, "\x00"+string(rune('A'+i))+"\x00", l, 1)
	}
	return out
}

func escapeMrkdwn(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// askBlocks renders mrkdwn text as section blocks of at most askSectionLimit characters, split at line breaks.
func askBlocks(text string) []slack.Block {
	var blocks []slack.Block
	for _, chunk := range chunkText(text, askSectionLimit) {
		if len(blocks) >= askMaxBlocks {
			break
		}
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, chunk, false, false), nil, nil))
	}
	return blocks
}

func chunkText(s string, limit int) []string {
	var out []string
	for len([]rune(s)) > limit {
		r := []rune(s)
		cut := limit
		if i := strings.LastIndex(string(r[:limit]), "\n"); i > limit/2 {
			cut = len([]rune(string(r[:limit])[:i]))
		}
		out = append(out, strings.TrimSpace(string(r[:cut])))
		s = strings.TrimSpace(string(r[cut:]))
	}
	if strings.TrimSpace(s) != "" {
		out = append(out, s)
	}
	return out
}

// plainFallback is the notification text of a message: its first characters, without markup.
func plainFallback(mrkdwn string) string {
	s := strings.ReplaceAll(mrkdwn, "*", "")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// confirmValue and parseConfirmValue carry what a confirmation button needs: the action and where the answer goes.
func confirmValue(kind, threadTS, channelKind string) string {
	return kind + "|" + threadTS + "|" + channelKind
}

func parseConfirmValue(v string) (kind, threadTS, channelKind string, ok bool) {
	parts := strings.Split(v, "|")
	if len(parts) != 3 || parts[0] == "" {
		return "", "", "", false
	}
	if parts[2] == AskChannelThread && parts[1] == "" { // a thread reply with no thread would land at a channel's top level
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// askActionLabels is what each action is called on a confirmation. The label comes from the action's kind, which is what
// runs when Run is pressed; nothing a model wrote can change it.
var askActionLabels = map[string]string{
	"play_next":   "Play the next event",
	"demo_status": "Show the replay status (read only)",
}

// askConfirmWhat is the exact action a confirmation shows: the fixed label of its kind and the target core read from the
// replay. ok is false for a kind this adapter does not know, which is never offered.
func askConfirmWhat(p AskProposedAction) (text string, ok bool) {
	label, known := askActionLabels[p.Kind]
	if !known {
		return "", false
	}
	if t := strings.TrimSpace(p.Target); t != "" {
		return "*" + label + "*\nOn: " + escapeMrkdwn(t), true
	}
	return "*" + label + "*", true
}

// askConfirmBlocks is the ephemeral confirmation: what will happen, and [Run] [Cancel].
func askConfirmBlocks(what, value string) []slack.Block {
	run := slack.NewButtonBlockElement(ActionAskRun, value, slack.NewTextBlockObject(slack.PlainTextType, "Run", false, false))
	run.Style = slack.StylePrimary
	cancel := slack.NewButtonBlockElement(ActionAskCancel, value, slack.NewTextBlockObject(slack.PlainTextType, "Cancel", false, false))
	return []slack.Block{
		slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, "*Confirm before I do this*\n"+what, false, false), nil, nil),
		slack.NewActionBlock(askBlockID, run, cancel),
	}
}
