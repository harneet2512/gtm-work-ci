package slacksurface

import (
	"strings"
	"testing"
)

const testWeb = "https://ghost.example.test/"

func TestEvalLinksPointAtTheWebEvalViews(t *testing.T) {
	f := NewFixture()
	cases := []struct{ got, want string }{
		{EvalsURL(testWeb, FixtureRunID, ""), "https://ghost.example.test/runs/" + FixtureRunID + "/evals"},
		{EvalsURL(testWeb, FixtureRunID, fixtureCandB), "https://ghost.example.test/runs/" + FixtureRunID + "/evals?candidate=" + fixtureCandB},
		{EvalResultURL(testWeb, FixtureRunID, "0e1a0000-0000-4000-8000-000000009201"), "https://ghost.example.test/runs/" + FixtureRunID + "/evals#result-0e1a0000-0000-4000-8000-000000009201"},
		{IntelligenceURL(testWeb, f.BI.AccountID), "https://ghost.example.test/accounts/" + f.BI.AccountID + "#intelligence-evals"},
		{MapURL(testWeb, f.BI), "https://ghost.example.test/accounts/" + f.BI.AccountID},
		{EvalsURL("", FixtureRunID, ""), ""},
		{EvalsURL("javascript:alert(1)", FixtureRunID, ""), ""},
		{EvalsURL(testWeb, "", ""), ""},
		{EvalResultURL(testWeb, FixtureRunID, ""), ""},
		{IntelligenceURL("ftp://x", f.BI.AccountID), ""},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %q, want %q", i, c.got, c.want)
		}
	}
	if got := EvalsURL(testWeb, "a b/c", "x&y"); got != "https://ghost.example.test/runs/a%20b%2Fc/evals?candidate=x%26y" {
		t.Errorf("ids are escaped: %s", got)
	}
}

// Every message links to the web eval view of its own eval job, and only when a web URL is configured.
func TestEveryMessageLinksToItsEvalsWhenTheWebIsConfigured(t *testing.T) {
	f := NewFixture()
	rs, dir := f.Strategies, f.Directory
	selected, err := RenderSelected(rs, f.Chosen, dir, "", testWeb)
	if err != nil {
		t.Fatal(err)
	}
	sent, _ := RenderSelected(rs, f.Sent, dir, "", testWeb)
	cases := []struct {
		name   string
		msg    Message
		action string
		url    string
	}{
		{"M1", RenderBI(f.BI, f.AccountName, MapURL(testWeb, f.BI), IntelligenceURL(testWeb, f.BI.AccountID)), ActionBIViewEvals, "/accounts/" + f.BI.AccountID + "#intelligence-evals"},
		{"M2 chooser", RenderChooser(rs, dir, testWeb), ActionStrategyViewEvals, "/runs/" + FixtureRunID + "/evals\""},
		{"M2 selected", selected, ActionSelectedViewEvals, "/runs/" + FixtureRunID + "/evals?candidate=" + f.Chosen.SelectedCandidateID},
		{"M2 sent", sent, ActionSelectedViewEvals, "/evals?candidate=" + f.Sent.SelectedCandidateID},
		{"M3", RenderJudgment(f.Inference, rs, FixtureRunID, testWeb), ActionJudgmentViewEvals, "/evals?candidate=" + f.Inference.HumanChoice},
		{"M3 confirmed", RenderJudgment(f.Confirmed, rs, FixtureRunID, testWeb), ActionJudgmentViewEvals, "/evals?candidate=" + f.Confirmed.HumanChoice},
	}
	for _, c := range cases {
		raw := mustJSON(c.msg)
		if !strings.Contains(raw, c.action) || !strings.Contains(raw, c.url) || !strings.Contains(raw, "View evals") {
			t.Errorf("%s: want a View evals button %s to %s", c.name, c.action, c.url)
		}
		if err := ValidateMessage(c.msg); err != nil {
			t.Errorf("%s breaks Slack limits: %v", c.name, err)
		}
	}

	noWeb, _ := RenderSelected(rs, f.Chosen, dir, "", "")
	for name, m := range map[string]Message{
		"M1":          RenderBI(f.BI, f.AccountName, "", ""),
		"M2 chooser":  RenderChooser(rs, dir, ""),
		"M2 selected": noWeb,
		"M3":          RenderJudgment(f.Inference, rs, FixtureRunID, ""),
	} {
		if raw := mustJSON(m); strings.Contains(raw, "View evals") || strings.Contains(raw, "|Evidence>") {
			t.Errorf("%s: no web URL, so no eval links", name)
		}
	}
}

func TestLinkButtonsAreOnlyAcknowledged(t *testing.T) {
	for _, id := range []string{ActionViewMap, ActionBIViewEvals, ActionStrategyViewEvals, ActionSelectedViewEvals, ActionJudgmentViewEvals} {
		if !isLinkAction(id) {
			t.Errorf("%s is a URL button and must only be acknowledged", id)
		}
	}
	if isLinkAction(ActionSelectedSend) {
		t.Fatal("Send is not a link")
	}
}
