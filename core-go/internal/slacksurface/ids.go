package slacksurface

import (
	"encoding/json"
	"errors"
)

// action_ids and callback_ids exactly as in contracts/slack/actions.md.
const (
	ActionViewMap          = "ghost.bi.view_map"
	ActionStrategyViewFull = "ghost.strategy.view_full"
	ActionStrategyChoose   = "ghost.strategy.choose"
	ActionSelectedEdit     = "ghost.selected.edit"
	ActionSelectedSend     = "ghost.selected.send"
	ActionSelectedViewFull = "ghost.selected.view_full"
	ActionJudgmentConfirm  = "ghost.judgment.confirm" // button label "Correct"
	ActionJudgmentCorrect  = "ghost.judgment.correct" // button label "Needs correction"
	ActionJudgmentNote     = "ghost.judgment.note"    // button label "Add note"

	// "View evals" URL buttons (contracts/slack/actions.md, View evals): acknowledged only.
	ActionBIViewEvals       = "ghost.bi.view_evals"
	ActionStrategyViewEvals = "ghost.strategy.view_evals"
	ActionSelectedViewEvals = "ghost.selected.view_evals"
	ActionJudgmentViewEvals = "ghost.judgment.view_evals"

	CallbackFullModal       = "ghost.strategy_full_modal"
	CallbackEditModal       = "ghost.edit_modal"
	CallbackCorrectionModal = "ghost.judgment_correction_modal"
	CallbackNoteModal       = "ghost.judgment_note_modal"

	// Modal input block_id / action_id pairs.
	inputTo         = "ghost.input.to"
	inputCC         = "ghost.input.cc"
	inputSubject    = "ghost.input.subject"
	inputBody       = "ghost.input.body"
	inputCorrection = "ghost.input.correction"
	inputNote       = "ghost.input.note"
	inputAction     = "value"
)

// Modal input ids, exported for drivers that submit modals (the no-human smoke run).
const (
	InputTo         = inputTo
	InputCC         = inputCC
	InputSubject    = inputSubject
	InputBody       = inputBody
	InputCorrection = inputCorrection
	InputNote       = inputNote
	InputAction     = inputAction
)

// Target is the small JSON of ids carried in a button value or a modal's private_metadata. It names
// things core owns; it is not state. ChannelID and MessageTS are only set in modal metadata, so a
// modal submission can update the message it came from (contracts/slack/actions.md).
type Target struct {
	RunID       string `json:"run_id,omitempty"`
	EpisodeID   string `json:"episode_id,omitempty"`
	CandidateID string `json:"candidate_id,omitempty"`
	ChannelID   string `json:"channel_id,omitempty"`
	MessageTS   string `json:"message_ts,omitempty"`
}

// Encode renders the target as the JSON string Slack carries back to us.
func (t Target) Encode() string {
	b, err := json.Marshal(t)
	if err != nil { // a struct of strings cannot fail; keep the signature simple
		return "{}"
	}
	return string(b)
}

// lockKey identifies what a write serialises on: the run for strategy actions, the episode for the
// judgment ones.
func (t Target) lockKey() string {
	if t.RunID != "" {
		return "run:" + t.RunID
	}
	return "episode:" + t.EpisodeID
}

// DecodeTarget parses a button value or private_metadata.
func DecodeTarget(s string) (Target, error) {
	var t Target
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		return Target{}, errors.New("slacksurface: malformed action target")
	}
	if t.RunID == "" && t.EpisodeID == "" {
		return Target{}, errors.New("slacksurface: action target has no run or episode")
	}
	return t, nil
}
