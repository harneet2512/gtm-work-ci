package slacksurface

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

// RenderSelected renders Message 2 after a choice: the chooser is updated in place into the expanded, already
// generated action (strategy, To/CC/Subject, full email, why, knowledge) with a concise eval summary, Edit, an
// explicit Send and Inspect evals (the exact run, chosen candidate and eval bundle on the web, when web is
// configured). Once sent or discarded Edit and Send are replaced by a status line. refusal, when not empty, is
// core's reason for refusing a Send (422), shown in place.
func RenderSelected(rs RunStrategies, dec HumanStrategyDecision, dir Directory, refusal, web string) (Message, error) {
	cand, ok := rs.Candidate(dec.SelectedCandidateID)
	if !ok {
		return Message{}, fmt.Errorf("slacksurface: selected candidate %q is not in strategy set", dec.SelectedCandidateID)
	}
	view := viewOf(cand, &dec, dir)
	id := "ghost.selected"
	blocks := []slack.Block{
		headerBlock(id+".header", "You selected "+optionLabel(rs, cand)),
		contextBlock(id+".pick", pickLine(rs, cand)),
		section(id+".intent", mrkdwn(truncate(cand.Description, 300))),
		fieldsSection(id+".recipients", [][2]string{{"To", joinAddrs(view.To)}, {"CC", joinAddrs(view.CC)}}),
		fieldsSection(id+".subject", [][2]string{{"Subject", view.Subject}}),
	}
	bodyBlocks, truncated := bodySections(id+".body", view.Body)
	blocks = append(blocks, bodyBlocks...)
	if len(dec.Edits) > 0 {
		blocks = append(blocks, contextBlock(id+".edited", "_Edited before send._"))
	}
	blocks = append(blocks, whyBlocks(id+".why", cand)...)
	runID := rs.StrategySet.AgentRunID
	blocks = append(blocks, section(id+".evals", "*Evals*\n"+conciseEvals(rs.Bundle(cand), len(dec.Edits) > 0)))
	evals := linkButton(ActionSelectedViewEvals, "Inspect evals", EvalsURL(web, runID, dec.SelectedCandidateID))
	blocks = append(blocks, selectedActions(id, runID, dec, view, truncated, refusal, evals)...)
	return Message{
		Text:   fmt.Sprintf("You selected %s - %s", optionLabel(rs, cand), truncate(view.Subject, 100)),
		Blocks: blocks,
	}, nil
}

// optionLabel is "B. <title>": the option's letter in the chooser and its title.
func optionLabel(rs RunStrategies, c StrategyCandidate) string {
	for i, x := range rs.StrategySet.Candidates {
		if x.CandidateID == c.CandidateID {
			return letter(i) + ". " + c.Title
		}
	}
	return c.Title
}

// pickLine says whether the human took my recommendation or another path: the premise of the interpretation
// Message 3 will offer.
func pickLine(rs RunStrategies, c StrategyCandidate) string {
	if c.PreferredByAgent {
		return "This is my recommendation."
	}
	for _, x := range rs.StrategySet.Candidates {
		if x.PreferredByAgent && !rs.StrategySet.NoAcceptableCandidate {
			return "I recommended " + mrkdwn(optionLabel(rs, x)) + "."
		}
	}
	return "I had recommended none of the options."
}

// bodySections shows the email in a code fence, truncated to draftTruncate characters.
func bodySections(blockID, body string) (blocks []slack.Block, truncated bool) {
	shown, esc := fitFenced(body, draftTruncate)
	out := []slack.Block{section(blockID, "*Full message*\n```"+esc+"```")}
	if shown != body {
		out = append(out, contextBlock(blockID+".cut",
			"_Shown truncated; choose View full for the complete email._"))
	}
	return out, shown != body
}

// fitFenced truncates body to at most limit characters whose escaped form still fits one section,
// and neutralises any triple backtick that would close the code fence early.
func fitFenced(body string, limit int) (shown, escaped string) {
	const room = maxSectionText - 60 // heading and fences
	shown = truncate(body, limit)
	for {
		escaped = mrkdwn(strings.ReplaceAll(shown, "```", "'''"))
		if utf8.RuneCountInString(escaped) <= room {
			return shown, escaped
		}
		shown = truncate(shown, utf8.RuneCountInString(shown)-50)
	}
}

// whyBlocks shows the rationale, the state fields and evidence quotes behind it, and the knowledge applied.
func whyBlocks(blockID string, c StrategyCandidate) []slack.Block {
	lines := []string{mrkdwn(c.Rationale)}
	if len(c.StateRefs) > 0 {
		lines = append(lines, "State used: "+mrkdwn(fieldWords(c.StateRefs)))
	}
	for _, e := range evidenceLines(c.EvidenceRefs) {
		lines = append(lines, "Evidence: "+e)
	}
	if k := knowledgeLine(c.KnowledgeRefs); k != "" {
		lines = append(lines, "Knowledge applied: "+k)
	} else {
		lines = append(lines, "Knowledge applied: none")
	}
	return textSections(blockID, "Why this strategy", bullets(lines, maxEvidence+4), 2)
}

func selectedActions(id, runID string, dec HumanStrategyDecision, view ArtView, truncated bool, refusal string, evals *slack.ButtonBlockElement) []slack.Block {
	t := Target{RunID: runID, CandidateID: dec.SelectedCandidateID}
	var els []slack.BlockElement
	if truncated {
		els = append(els, button(ActionSelectedViewFull, "View full", t))
	}
	els = withLink(els, evals)
	if dec.SendDecision == SendSend || dec.SendDecision == SendDiscard {
		out := []slack.Block{}
		if len(els) > 0 {
			out = append(out, actions(id+".actions", els...))
		}
		status := "Sent"
		if dec.SendDecision == SendDiscard {
			status = "Discarded"
		}
		if dec.ActorLabel != "" {
			status += " by " + mrkdwn(dec.ActorLabel)
		}
		if dec.SendDecidedAt != nil {
			status += " at " + dec.SendDecidedAt.UTC().Format("2006-01-02 15:04 MST")
		}
		return append(out, contextBlock(id+".status", "*"+status+"*"))
	}
	send := button(ActionSelectedSend, "Send", Target{RunID: runID}).WithStyle(slack.StylePrimary)
	send.Confirm = slack.NewConfirmationBlockObject(plain("Send this email?"), md(confirmText(view)), plain("Send"), plain("Cancel"))
	edit := button(ActionSelectedEdit, "Edit", Target{RunID: runID})
	// The two decisions first (Edit, the explicit Send), then the links (View full, View evals).
	els = append([]slack.BlockElement{edit, send}, els...)
	blocks := []slack.Block{actions(id+".actions", els...)}
	if r := strings.TrimSpace(refusal); r != "" {
		blocks = append(blocks, contextBlock(id+".refused", "_Send was refused: "+mrkdwn(truncate(r, 300))+"_"))
	}
	return blocks
}

// confirmText stays under Slack's 300 character limit for a confirm dialog's text, after escaping.
func confirmText(view ArtView) string {
	text := "This sends *" + mrkdwn(truncate(view.Subject, 80)) + "* to " + mrkdwn(truncate(joinAddrs(view.To), 100)) + "."
	return truncate(text, maxConfirmText)
}
