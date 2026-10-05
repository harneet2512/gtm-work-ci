package slacksurface

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/slack-go/slack"
)

// Preview is one named Block Kit payload.
type Preview struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // "message" or "modal"
	Payload any    `json:"payload"`
}

const (
	previewChannel = "C0DRYRUN"
	previewTS      = "1700000000.000100"
	previewWebURL  = "https://ghost.example.test"
)

// Previews renders every message state and modal of the fixture episode, in demo order. It is pure
// and offline; the golden tests and --dry-run both use it. Every payload is checked against Slack's
// limits before it is returned.
func Previews(f Fixture) ([]Preview, error) {
	rs, dir := f.Strategies, f.Directory
	var out []Preview
	add := func(name string, m Message) error {
		if err := ValidateMessage(m); err != nil {
			return fmt.Errorf("%s breaks Slack limits: %w", name, err)
		}
		out = append(out, Preview{Name: name, Kind: "message", Payload: m})
		return nil
	}
	if err := add("message1_business_intelligence", RenderBI(f.BI, f.AccountName, MapURL(previewWebURL, f.BI), IntelligenceURL(previewWebURL, f.BI.AccountID))); err != nil {
		return nil, err
	}
	if err := add("message2_chooser", RenderChooser(rs, dir, previewWebURL)); err != nil {
		return nil, err
	}
	for _, s := range []struct {
		name string
		dec  HumanStrategyDecision
	}{{"message2_selected", f.Chosen}, {"message2_selected_edited", f.Edited}, {"message2_sent", f.Sent}} {
		m, err := RenderSelected(rs, s.dec, dir, "", previewWebURL)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", s.name, err)
		}
		if err := add(s.name, m); err != nil {
			return nil, err
		}
	}
	refused, err := RenderSelected(rs, f.Edited, dir, "a blocking eval is unresolved", previewWebURL)
	if err != nil {
		return nil, err
	}
	if err := add("message2_send_refused", refused); err != nil {
		return nil, err
	}
	for _, s := range []struct {
		name string
		inf  JudgmentInference
	}{{"message3_judgment", f.Inference}, {"message3_judgment_confirmed", f.Confirmed}, {"message3_judgment_corrected", f.Corrected}} {
		if err := add(s.name, RenderJudgment(s.inf, rs, FixtureRunID, previewWebURL)); err != nil {
			return nil, err
		}
	}
	return previewModals(f, out)
}

func previewModals(f Fixture, out []Preview) ([]Preview, error) {
	rs, dir := f.Strategies, f.Directory
	cand, _ := rs.Candidate(f.Chosen.SelectedCandidateID)
	t := Target{RunID: FixtureRunID, CandidateID: cand.CandidateID, ChannelID: previewChannel, MessageTS: previewTS}
	tj := Target{RunID: FixtureRunID, EpisodeID: FixtureEpisodeID, ChannelID: previewChannel, MessageTS: "1700000000.000300"}
	for _, m := range []struct {
		name string
		v    slack.ModalViewRequest
	}{
		{"modal_view_full", RenderFullModal(cand, rs.Bundle(cand), viewOf(cand, nil, dir), t)},
		{"modal_edit", RenderEditModal(viewOf(cand, &f.Edited, dir), t)},
		{"modal_needs_correction", RenderCorrectionModal(f.Inference, tj)},
		{"modal_add_note", RenderNoteModal(tj)},
	} {
		if err := ValidateModal(m.v); err != nil {
			return nil, fmt.Errorf("%s breaks Slack limits: %w", m.name, err)
		}
		out = append(out, Preview{Name: m.name, Kind: "modal", Payload: m.v})
	}
	return out, nil
}

// WritePreviews writes the previews as indented JSON.
func WritePreviews(w io.Writer, ps []Preview) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ps)
}
