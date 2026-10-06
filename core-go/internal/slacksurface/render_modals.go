package slacksurface

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

const (
	modalBodyChunk    = 2400 // raw characters per section; leaves room for escaping and fences
	modalBodySections = 30
)

func modal(title, callbackID string, t Target, blocks []slack.Block) slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:            slack.VTModal,
		Title:           plain(title),
		Close:           plain("Close"),
		CallbackID:      callbackID,
		PrivateMetadata: t.Encode(),
		Blocks:          slack.Blocks{BlockSet: blocks},
	}
}

// RenderFullModal opens the complete email/action and deeper rationale for one candidate: full
// artifact, rationale, evidence, knowledge used and every relevant eval with its reason. view is what
// to show (the candidate's artifact, or the edited final one once chosen). It only reads data that
// already exists: it never triggers generation.
func RenderFullModal(c StrategyCandidate, bundle EvalBundle, view ArtView, t Target) slack.ModalViewRequest {
	id := "ghost.full"
	blocks := []slack.Block{
		headerBlock(id+".header", c.Title),
		section(id+".intent", mrkdwn(truncate(c.Description, 600))),
		artifactFields(id+".addr", view),
	}
	for i, ch := range splitText(view.Body, modalBodyChunk) {
		if i == modalBodySections {
			break
		}
		title := ""
		if i == 0 {
			title = "*Full message*\n"
		}
		blocks = append(blocks, section(id+".body."+strconv.Itoa(i), title+"```"+mrkdwn(strings.ReplaceAll(ch, "```", "'''"))+"```"))
	}
	blocks = append(blocks, whyBlocks(id+".why", c)...)
	blocks = append(blocks, evalSections(id+".evals", bundle, t.RunID, "", false, 20)...)
	return modal("Full draft", CallbackFullModal, t, blocks)
}

func textInput(blockID, label, initial string, multiline, optional bool, max int) *slack.InputBlock {
	el := slack.NewPlainTextInputBlockElement(nil, inputAction)
	el.Multiline = multiline
	el.InitialValue = initial
	el.MaxLength = max
	in := slack.NewInputBlock(blockID, plain(label), nil, el)
	in.Optional = optional
	return in
}

// Field limits of the Edit modal; Slack text inputs hold at most 3,000 characters.
const (
	maxEditAddrs   = maxInputInitial
	maxEditSubject = 1000
)

// RenderEditModal opens To / CC / Subject / body for editing. Recipients are shown as
// "Name <email>" lines. If any field is longer than Slack's input can hold, the modal says so and
// offers no inputs, rather than silently truncating the email.
func RenderEditModal(view ArtView, t Target) slack.ModalViewRequest {
	to, cc := strings.Join(view.To, ", "), strings.Join(view.CC, ", ")
	tooLong := utf8.RuneCountInString(view.Body) > maxInputInitial ||
		utf8.RuneCountInString(to) > maxEditAddrs || utf8.RuneCountInString(cc) > maxEditAddrs ||
		utf8.RuneCountInString(view.Subject) > maxEditSubject
	if tooLong {
		return modal("Edit email", CallbackEditModal, t, []slack.Block{section("ghost.edit.toolong",
			"This email is longer than Slack's input limits, so it cannot be edited here. "+
				"Use the web view; nothing was changed.")})
	}
	v := modal("Edit email", CallbackEditModal, t, []slack.Block{
		textInput(inputTo, "To (people on this account, comma separated)", to, false, false, maxEditAddrs),
		textInput(inputCC, "CC (comma separated)", cc, false, true, maxEditAddrs),
		textInput(inputSubject, "Subject", view.Subject, false, false, maxEditSubject),
		textInput(inputBody, "Body", view.Body, true, false, maxInputInitial),
	})
	v.Submit = plain("Save")
	return v
}

// LoadingModal is opened immediately, while the trigger_id is fresh, and replaced by UpdateView
// once core has answered.
func LoadingModal(t Target) slack.ModalViewRequest {
	return modal("Loading", CallbackFullModal, t, []slack.Block{section("ghost.loading", "Loading...")})
}

// ErrorModal replaces a loading modal when core could not answer.
func ErrorModal(t Target) slack.ModalViewRequest {
	return modal("Not available", CallbackFullModal, t, []slack.Block{section("ghost.error",
		"Cliff could not load this right now. Close this and try again.")})
}

// RenderCorrectionModal is Edit interpretation: the input starts from my interpretation and the human rewrites it
// into what they meant. The edit is stored as the correction, the optional note beside it.
func RenderCorrectionModal(inf JudgmentInference, t Target) slack.ModalViewRequest {
	v := modal("Edit interpretation", CallbackCorrectionModal, t, []slack.Block{
		section("ghost.correction.intro", "Change my interpretation so it says what you meant. It is saved as your correction."),
		textInput(inputCorrection, "My interpretation", truncate(inf.InferredSemanticDelta.Statement, maxInputInitial), true, false, maxInputInitial),
		textInput(inputNote, "Note (optional)", "", true, true, maxInputInitial),
	})
	v.Submit = plain("Save")
	return v
}

// RenderNoteModal collects a free-text note on the judgment.
func RenderNoteModal(t Target) slack.ModalViewRequest {
	v := modal("Add note", CallbackNoteModal, t, []slack.Block{
		textInput(inputNote, "Note", "", true, false, maxInputInitial),
	})
	v.Submit = plain("Save")
	return v
}
