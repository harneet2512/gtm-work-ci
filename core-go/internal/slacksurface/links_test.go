package slacksurface

import (
	"context"
	"encoding/json"
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
		{TraceURL(testWeb, FixtureEpisodeID, ""), "https://ghost.example.test/episodes/" + FixtureEpisodeID},
		{AccountURL(testWeb, f.BI.AccountID), "https://ghost.example.test/accounts/" + f.BI.AccountID},
		{EvalsURL("", FixtureRunID, ""), ""},
		{EvalsURL("javascript:alert(1)", FixtureRunID, ""), ""},
		{EvalsURL(testWeb, "", ""), ""},
		{EvalResultURL(testWeb, FixtureRunID, ""), ""},
		{AccountURL("ftp://x", f.BI.AccountID), ""},
		{TraceURL("", FixtureEpisodeID, ""), ""},
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

// Messages 2 and 3 link to the web eval view of the decision loop (Message 1 links to the account and trace
// instead, see cliff_test.go), and only when a web URL is configured.
func TestMessagesTwoAndThreeLinkToTheirEvalsWhenTheWebIsConfigured(t *testing.T) {
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
		{"M2 chooser", RenderChooser(rs, testWeb), ActionStrategyViewEvals, "/runs/" + FixtureRunID + "/evals\""},
		{"M2 selected", selected, ActionSelectedViewEvals, "/runs/" + FixtureRunID + "/evals?candidate=" + f.Chosen.SelectedCandidateID},
		{"M2 sent", sent, ActionSelectedViewEvals, "/evals?candidate=" + f.Sent.SelectedCandidateID},
	}
	for _, c := range cases {
		raw := mustJSON(c.msg)
		if !strings.Contains(raw, c.action) || !strings.Contains(raw, c.url) || !(strings.Contains(raw, "View evals") || strings.Contains(raw, "Inspect evals")) {
			t.Errorf("%s: want an eval button %s to %s", c.name, c.action, c.url)
		}
		if err := ValidateMessage(c.msg); err != nil {
			t.Errorf("%s breaks Slack limits: %v", c.name, err)
		}
	}

	noWeb, _ := RenderSelected(rs, f.Chosen, dir, "", "")
	for name, m := range map[string]Message{
		"M2 chooser":  RenderChooser(rs, ""),
		"M2 selected": noWeb,
		"M3":          RenderJudgment(f.Inference, rs, FixtureRunID, ""),
	} {
		if raw := mustJSON(m); strings.Contains(raw, "View evals") || strings.Contains(raw, "Inspect evals") || strings.Contains(raw, "|Evidence>") {
			t.Errorf("%s: no web URL, so no eval links", name)
		}
	}
}

func TestLinkButtonsAreOnlyAcknowledged(t *testing.T) {
	for _, id := range []string{ActionBIViewAccount, ActionBIViewTrace, ActionStrategyViewEvals, ActionSelectedViewEvals, ActionJudgmentViewEvals} {
		if !isLinkAction(id) {
			t.Errorf("%s is a URL button and must only be acknowledged", id)
		}
	}
	if isLinkAction(ActionSelectedSend) {
		t.Fatal("Send is not a link")
	}
}

// The episode page resolves Message 1 only when the link carries the replay manifest, so View trace names it
// whenever it is known and is the plain episode link otherwise.
func TestViewTraceCarriesTheReplayManifestWhenKnown(t *testing.T) {
	const manifest = "0aa00000-0000-4000-8000-0000000000aa"
	cases := []struct{ got, want string }{
		{TraceURL(testWeb, FixtureEpisodeID, manifest), "https://ghost.example.test/episodes/" + FixtureEpisodeID + "?manifest=" + manifest},
		{TraceURL(testWeb, FixtureEpisodeID, ""), "https://ghost.example.test/episodes/" + FixtureEpisodeID},
		{TraceURL(testWeb, FixtureEpisodeID, "a&b c"), "https://ghost.example.test/episodes/" + FixtureEpisodeID + "?manifest=a%26b+c"},
		{TraceURL("", FixtureEpisodeID, manifest), ""},
		{TraceURL(testWeb, "", manifest), ""},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %q, want %q", i, c.got, c.want)
		}
	}
}

func TestMessage1PostedByAPublisherThatKnowsTheManifestLinksToIt(t *testing.T) {
	const manifest = "0aa00000-0000-4000-8000-0000000000aa"
	poster := &fakePoster{}
	pub := NewPublisher(newMemCore(), poster, "C0TEST", testWeb).WithManifest(manifest)

	if _, err := pub.PostBIForEpisode(context.Background(), "a", FixtureEpisodeID); err != nil {
		t.Fatal(err)
	}
	if len(poster.posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(poster.posts))
	}
	raw, err := json.Marshal(poster.posts[0].Blocks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "/episodes/"+FixtureEpisodeID+"?manifest="+manifest) {
		t.Fatalf("View trace does not carry the manifest: %+v", poster.posts)
	}
}
