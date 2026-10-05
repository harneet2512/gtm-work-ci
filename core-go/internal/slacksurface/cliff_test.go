package slacksurface

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

// Cliff (HAR-129 FINAL DEMO SURFACES): the three messages in the agent's voice, what each button links to and
// does, and the no-learning verdict. Block JSON of every state is pinned by the goldens in render_test.go.

const testEpisode = "0e9e0000-0000-4000-8000-000000000a01"

// buttonsOf lists the buttons of every actions block of a message, in order.
func buttonsOf(m Message) []*slack.ButtonBlockElement {
	var out []*slack.ButtonBlockElement
	for _, b := range m.Blocks {
		if ab, ok := b.(*slack.ActionBlock); ok {
			for _, el := range ab.Elements.ElementSet {
				if btn, ok := el.(*slack.ButtonBlockElement); ok {
					out = append(out, btn)
				}
			}
		}
	}
	return out
}

func labelsOf(bs []*slack.ButtonBlockElement) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Text.Text
	}
	return out
}

func buttonByAction(t *testing.T, m Message, actionID string) *slack.ButtonBlockElement {
	t.Helper()
	for _, b := range buttonsOf(m) {
		if b.ActionID == actionID {
			return b
		}
	}
	t.Fatalf("no %s button; have %v", actionID, labelsOf(buttonsOf(m)))
	return nil
}

// blockIDs lists the block ids of a message, in order.
func blockIDs(m Message) []string {
	raw, _ := json.Marshal(m.Blocks)
	var bs []struct {
		BlockID string `json:"block_id"`
	}
	_ = json.Unmarshal(raw, &bs)
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.BlockID
	}
	return out
}

func indexOf(xs []string, x string) int {
	for i, y := range xs {
		if y == x {
			return i
		}
	}
	return -1
}

// ---- identity -------------------------------------------------------------------------------------------------

func TestManifestNamesTheAppCliff(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "slack", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(raw)
	for _, want := range []string{"name: Cliff\n", "description: GTM Intelligence Agent\n", "display_name: Cliff\n"} {
		if !strings.Contains(strings.ReplaceAll(manifest, "\r\n", "\n"), want) {
			t.Errorf("manifest lacks %q", want)
		}
	}
	if strings.Contains(manifest, "Ghost Demo") || strings.Contains(manifest, "display_name: ghost-demo") {
		t.Error("the manifest still names the app Ghost Demo")
	}
}

// ---- Message 1 --------------------------------------------------------------------------------------------------

func TestM1LayoutIsChangedThenWhatChangedWhyItMattersEvidence(t *testing.T) {
	f := NewFixture()
	m := RenderBI(f.BI, f.AccountName, AccountURL(testWeb, f.BI.AccountID), TraceURL(testWeb, testEpisode, ""))
	ids := blockIDs(m)

	order := []string{"ghost.bi.header", "ghost.bi.changes", "ghost.bi.why", "ghost.bi.evidence", "ghost.bi.actions"}
	prev := -1
	for _, id := range order {
		i := indexOf(ids, id)
		if i < 0 {
			t.Fatalf("M1 lacks block %s; has %v", id, ids)
		}
		if i <= prev {
			t.Fatalf("block %s is out of order in %v", id, ids)
		}
		prev = i
	}
	raw := mustJSON(m)
	for _, want := range []string{f.AccountName + " changed", "What changed", "Why it matters", "Evidence"} {
		if !strings.Contains(raw, want) {
			t.Errorf("M1 lacks the heading %q", want)
		}
	}
	if err := ValidateMessage(m); err != nil {
		t.Fatal(err)
	}
}

func TestM1ButtonsAreViewAccountAndViewTrace(t *testing.T) {
	f := NewFixture()
	m := RenderBI(f.BI, f.AccountName, AccountURL(testWeb, f.BI.AccountID), TraceURL(testWeb, testEpisode, ""))

	if got := labelsOf(buttonsOf(m)); strings.Join(got, "|") != "View account|View trace" {
		t.Fatalf("buttons = %v, want [View account] [View trace]", got)
	}
	if b := buttonByAction(t, m, ActionBIViewAccount); b.URL != "https://ghost.example.test/accounts/"+f.BI.AccountID {
		t.Errorf("View account links to %s", b.URL)
	}
	if b := buttonByAction(t, m, ActionBIViewTrace); b.URL != "https://ghost.example.test/episodes/"+testEpisode {
		t.Errorf("View trace links to %s, want the episode the control plane shows", b.URL)
	}
	if strings.Contains(mustJSON(m), "View evals") {
		t.Error("M1 has no View evals button any more")
	}
}

func TestM1ViewTraceWaitsForTheEpisodeAndTheWebURL(t *testing.T) {
	f := NewFixture()
	account := AccountURL(testWeb, f.BI.AccountID)

	noEpisode := RenderBI(f.BI, f.AccountName, account, TraceURL(testWeb, "", ""))
	if got := labelsOf(buttonsOf(noEpisode)); strings.Join(got, "|") != "View account" {
		t.Errorf("before the episode exists, buttons = %v, want View account only", got)
	}
	noWeb := RenderBI(f.BI, f.AccountName, AccountURL("", f.BI.AccountID), TraceURL("", testEpisode, ""))
	if len(buttonsOf(noWeb)) != 0 || indexOf(blockIDs(noWeb), "ghost.bi.actions") >= 0 {
		t.Errorf("without a web URL M1 has no buttons, got %v", labelsOf(buttonsOf(noWeb)))
	}
	if TraceURL("javascript:alert(1)", testEpisode, "") != "" || TraceURL(testWeb, "a b/c", "") != "https://ghost.example.test/episodes/a%20b%2Fc" {
		t.Error("TraceURL must refuse unsafe bases and escape the id")
	}
}

// ---- Message 2 --------------------------------------------------------------------------------------------------

func TestM2ChooserOpensWithThreeReasonablePathsAndMarksTheRecommendedOne(t *testing.T) {
	f := NewFixture()
	m := RenderChooser(f.Strategies, testWeb)

	ids := blockIDs(m)
	if ids[0] != "ghost.strategy.header" {
		t.Fatalf("first block = %s", ids[0])
	}
	raw := mustJSON(m)
	if !strings.Contains(raw, "I see 3 reasonable paths.") {
		t.Fatal("the chooser must open with \"I see 3 reasonable paths.\"")
	}
	if n := strings.Count(raw, "Recommended"); n != 1 {
		t.Fatalf("Recommended appears %d times, want once, on the preferred option", n)
	}
	card := mustJSON(blocksWithID(m, "ghost.strategy.card."+fixtureCandA))
	if !strings.Contains(card, "A.") || !strings.Contains(card, "Recommended") {
		t.Errorf("option A (the agent's preference) must carry the Recommended mark: %s", card)
	}
	if got := labelsOf(buttonsOf(m)); strings.Join(got, "|") != "View full|Select A|View full|Select B|View full|Select C|View evals" {
		t.Errorf("buttons = %v", got)
	}
	if strings.Contains(raw, ActionSelectedSend) {
		t.Error("the chooser must not offer Send before a choice")
	}
}

// blocksWithID returns the blocks whose block_id equals id.
func blocksWithID(m Message, id string) []slack.Block {
	var out []slack.Block
	for i, bid := range blockIDs(m) {
		if bid == id {
			out = append(out, m.Blocks[i])
		}
	}
	return out
}

func TestM2ChooserMarksNoneWhenNoCandidateIsAcceptable(t *testing.T) {
	f := NewFixture()
	f.Strategies.StrategySet.NoAcceptableCandidate = true
	raw := mustJSON(RenderChooser(f.Strategies, ""))

	if strings.Contains(raw, "Recommended") {
		t.Fatal("nothing is acceptable, so nothing is recommended")
	}
	if !strings.Contains(raw, "I see 3 reasonable paths.") || !strings.Contains(raw, "I recommend none") {
		t.Fatal("the chooser still opens the same way and says nothing is recommended")
	}
}

func TestM2SelectedExpandsInPlaceWithAConciseEvalSummaryAndInspectEvals(t *testing.T) {
	f := NewFixture()
	m, err := RenderSelected(f.Strategies, f.Chosen, f.Directory, "", testWeb)
	if err != nil {
		t.Fatal(err)
	}
	raw := mustJSON(m)

	if !strings.Contains(raw, "You selected B.") {
		t.Errorf("the expanded header names the selected option: %s", raw)
	}
	b := buttonByAction(t, m, ActionSelectedViewEvals)
	if b.Text.Text != "Inspect evals" || b.URL != "https://ghost.example.test/runs/"+FixtureRunID+"/evals?candidate="+fixtureCandB {
		t.Errorf("Inspect evals = %q -> %s, want the run's eval page for the chosen candidate", b.Text.Text, b.URL)
	}
	// The summary is the shared Level 1 line; the per-eval list belongs to the web eval page.
	bundle, _ := f.Strategies.Candidate(fixtureCandB)
	want := evalSummary(f.Strategies.Bundle(bundle))
	if !strings.Contains(raw, strings.ReplaceAll(want, `"`, `\"`)) {
		t.Errorf("the summary %q is missing", want)
	}
	if strings.Contains(raw, "Evals for this action") || strings.Contains(raw, "|Evidence>") {
		t.Error("the expanded state has a concise summary, not the per-eval list")
	}
	if got := labelsOf(buttonsOf(m)); strings.Join(got, "|") != "Edit|Send|Inspect evals" {
		t.Errorf("buttons = %v: Edit and the explicit Send stay, then Inspect evals", got)
	}
	if send := buttonByAction(t, m, ActionSelectedSend); send.Confirm == nil {
		t.Error("Send must keep its confirmation")
	}
}

func TestM2SelectedSaysWhenItIsNotTheRecommendation(t *testing.T) {
	f := NewFixture() // the human chose B, the agent preferred A
	m, _ := RenderSelected(f.Strategies, f.Chosen, f.Directory, "", "")
	if !strings.Contains(mustJSON(m), "I recommended A.") {
		t.Error("choosing against the recommendation is said, so the later interpretation has its premise")
	}
	same := f.Chosen
	same.SelectedCandidateID = fixtureCandA
	m, _ = RenderSelected(f.Strategies, same, f.Directory, "", "")
	if !strings.Contains(mustJSON(m), "my recommendation") {
		t.Error("choosing the recommendation is said too")
	}
}

func TestM2SelectedKeepsTheEditedNoteAndFlagsBlockingFailures(t *testing.T) {
	f := NewFixture()
	edited, _ := RenderSelected(f.Strategies, f.Edited, f.Directory, "", "")
	if !strings.Contains(mustJSON(edited), "Not re-evaluated yet") {
		t.Error("an edited draft says its verdicts were not re-evaluated")
	}
	cand, _ := f.Strategies.Candidate(fixtureCandB)
	for i := range f.Strategies.EvalBundles {
		if f.Strategies.EvalBundles[i].ID == cand.EvalBundleRef {
			f.Strategies.EvalBundles[i].Items[0].Verdict = VerdictFail
			f.Strategies.EvalBundles[i].Items[0].Result.Blocking = true
		}
	}
	blocked, _ := RenderSelected(f.Strategies, f.Chosen, f.Directory, "", "")
	if !strings.Contains(mustJSON(blocked), "Blocks send") {
		t.Error("a blocking failure is named in the summary")
	}
}
