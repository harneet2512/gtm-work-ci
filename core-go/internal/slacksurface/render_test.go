package slacksurface

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestGoldenPreviews pins the exact Block Kit JSON of every message state and modal.
// Regenerate deliberately with: go test ./internal/slacksurface -run Golden -update
func TestGoldenPreviews(t *testing.T) {
	ps, err := Previews(NewFixture())
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{
		"message1_business_intelligence", "message2_chooser", "message2_selected", "message2_selected_edited",
		"message2_sent", "message2_send_refused", "message3_judgment", "message3_judgment_confirmed",
		"message3_judgment_corrected", "modal_view_full", "modal_edit", "modal_needs_correction", "modal_add_note",
	}
	if len(ps) != len(wantNames) {
		t.Fatalf("got %d previews, want %d", len(ps), len(wantNames))
	}
	for i, p := range ps {
		if p.Name != wantNames[i] {
			t.Fatalf("preview %d is %s, want %s", i, p.Name, wantNames[i])
		}
		t.Run(p.Name, func(t *testing.T) {
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetIndent("", "  ")
			if err := enc.Encode(p.Payload); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", "golden", p.Name+".json")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing golden file (run with -update): %v", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), buf.Bytes()) {
				t.Fatalf("%s differs from golden file %s; if intended, rerun with -update", p.Name, path)
			}
		})
	}
}

func str(s string) *string { return &s }

func nastyFixture() Fixture {
	f := NewFixture()
	huge := strings.Repeat("Line with <!channel> & <@U123> and ```fence``` text. ", 400) // ~21k chars
	cands := f.Strategies.StrategySet.Candidates
	for i := range cands {
		c := &cands[i]
		c.FullActionArtifact.Body, c.Preview, c.Rationale, c.Description = huge, huge, huge, huge
		c.Title = strings.Repeat("S", 400)
		c.FullActionArtifact.Subject = str(strings.Repeat("Subject ", 100))
		for j := 0; j < 30; j++ {
			c.EvidenceRefs = append(c.EvidenceRefs, EvidenceRef{ActivityID: "a", Quote: huge})
		}
		c.KnowledgeRefs = append(c.KnowledgeRefs, "0c17c000-0000-4000-8000-000000000017")
	}
	for i := range f.Strategies.EvalBundles {
		for j := 0; j < 60; j++ {
			f.Strategies.EvalBundles[i].Items = append(f.Strategies.EvalBundles[i].Items,
				EvalItem{EvalType: "cta_calibration", Verdict: VerdictWarn, Result: &EvalResult{Reason: huge, SuggestedCorrection: str(huge)}})
		}
	}
	f.BI.Summary, f.BI.WhyItMatters = huge, huge
	for j := 0; j < 40; j++ {
		f.BI.Claims = append(f.BI.Claims, Claim{Statement: huge, EvidenceRefs: []EvidenceRef{{ActivityID: "x", Quote: huge}}})
	}
	for _, inf := range []*JudgmentInference{&f.Inference, &f.Corrected} {
		inf.InferredSemanticDelta.Statement = huge
		for j := 0; j < 40; j++ {
			inf.Evidence.CandidateDifferences = append(inf.Evidence.CandidateDifferences, huge)
		}
	}
	f.Corrected.HumanNote, f.Corrected.CorrectedStatement = str(huge), str(huge)
	return f
}

// TestHostileInputStaysWithinSlackLimits feeds oversized, markup-heavy content through every
// renderer and checks the output against Slack's documented limits.
func TestHostileInputStaysWithinSlackLimits(t *testing.T) {
	ps, err := Previews(nastyFixture()) // Previews validates every message and modal
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		raw, _ := json.Marshal(p.Payload)
		if bytes.Contains(raw, []byte("<!channel>")) || bytes.Contains(raw, []byte("<@U123>")) {
			t.Fatalf("%s leaks an unescaped mention/broadcast", p.Name)
		}
	}
}

func TestSelectedMessageTruncatesDraftAndModalHoldsAllOfIt(t *testing.T) {
	f := NewFixture()
	body := strings.Repeat("0123456789", 500) // 5,000 characters
	f.Strategies.StrategySet.Candidates[1].FullActionArtifact.Body = body
	msg, err := RenderSelected(f.Strategies, f.Chosen, f.Directory, "")
	if err != nil {
		t.Fatal(err)
	}
	raw := mustJSON(msg)
	if strings.Contains(raw, body) || !strings.Contains(raw, "View full") {
		t.Fatal("a 5,000 character body must be truncated in the channel and offer View full")
	}
	if n := strings.Count(raw, "0123456789"); n > draftTruncate/10+1 {
		t.Fatalf("channel message shows %d characters of body, cap is %d", n*10, draftTruncate)
	}
	c, _ := f.Strategies.Candidate(fixtureCandB)
	modalRaw := mustJSON(RenderFullModal(c, f.Strategies.Bundle(c), viewOf(c, nil, f.Directory), Target{RunID: "r"}))
	if got := strings.Count(modalRaw, "0123456789"); got != 500 {
		t.Fatalf("modal holds %d of 500 body chunks", got)
	}
}

func TestChooserHasThreeCardsAndNoSend(t *testing.T) {
	f := NewFixture()
	m := RenderChooser(f.Strategies, f.Directory)
	if err := ValidateMessage(m); err != nil {
		t.Fatal(err)
	}
	raw := mustJSON(m)
	for _, want := range []string{ActionStrategyViewFull, ActionStrategyChoose, "Ghost's pick", "ghost.strategy.card." + fixtureCandA,
		"ghost.strategy.actions." + fixtureCandB, "Ghost currently prefers", "ghost.strategy.header"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("chooser lacks %q", want)
		}
	}
	if strings.Contains(raw, ActionSelectedSend) {
		t.Fatal("the chooser must not offer Send before a choice")
	}
	if strings.Contains(raw, "Economic buyer coverage") {
		t.Fatal("not_relevant evals must be omitted from the card")
	}
}

func TestChooserRecommendsNoneWhenNoCandidateIsAcceptable(t *testing.T) {
	f := NewFixture()
	f.Strategies.StrategySet.NoAcceptableCandidate = true
	m := RenderChooser(f.Strategies, f.Directory)
	if err := ValidateMessage(m); err != nil {
		t.Fatal(err)
	}
	raw := mustJSON(m)
	if strings.Contains(raw, "Ghost's pick") || strings.Contains(raw, "Ghost currently prefers") {
		t.Fatal("no candidate is recommended, so no pick may be shown")
	}
	if !strings.Contains(raw, "recommends none") || !strings.Contains(raw, ActionStrategyChoose) {
		t.Fatal("the chooser must say nothing is recommended and still let the human choose")
	}
}

func TestSelectedUnknownCandidateIsAnError(t *testing.T) {
	f := NewFixture()
	if _, err := RenderSelected(f.Strategies, HumanStrategyDecision{SelectedCandidateID: "nope"}, f.Directory, ""); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDiscardedStateHasNoButtons(t *testing.T) {
	f := NewFixture()
	d := f.Sent
	d.SendDecision = SendDiscard
	m, _ := RenderSelected(f.Strategies, d, f.Directory, "")
	if raw := mustJSON(m); !strings.Contains(raw, "Discarded by alex") || strings.Contains(raw, ActionSelectedSend) {
		t.Fatalf("discard render wrong: %s", raw)
	}
}

func TestBIWithoutWebURLHasNoMapButton(t *testing.T) {
	f := NewFixture()
	raw := mustJSON(RenderBI(f.BI, f.AccountName, ""))
	if strings.Contains(raw, ActionViewMap) {
		t.Fatal("no web URL configured: the map button must be omitted")
	}
	if MapURL("javascript:alert(1)", f.BI) != "" || !strings.Contains(MapURL("https://x.test/", f.BI), "/accounts/"+f.BI.AccountID+"/map") {
		t.Fatal("MapURL")
	}
}

func TestEditModalRefusesBodiesSlackCannotHold(t *testing.T) {
	v := RenderEditModal(ArtView{Body: strings.Repeat("x", maxInputInitial+1)}, Target{EpisodeID: "e"})
	if v.Submit != nil || strings.Contains(mustJSON(v), inputBody) {
		t.Fatal("an over-long body must not produce an editable (truncating) input")
	}
	if err := ValidateModal(v); err != nil {
		t.Fatal(err)
	}
}

func TestValidatorCatchesViolations(t *testing.T) {
	long := strings.Repeat("x", maxSectionText+1)
	cases := map[string]string{
		"section too long": `[{"type":"section","text":{"type":"mrkdwn","text":"` + long + `"}}]`,
		"empty text":       `[{"type":"section","text":{"type":"mrkdwn","text":""}}]`,
		"header too long":  `[{"type":"header","text":{"type":"plain_text","text":"` + strings.Repeat("h", 151) + `"}}]`,
		"duplicate id":     `[{"type":"divider","block_id":"a"},{"type":"divider","block_id":"a"}]`,
		"button too long":  `[{"type":"actions","elements":[{"type":"button","text":{"type":"plain_text","text":"` + strings.Repeat("b", 76) + `"}}]}]`,
		"value too long":   `[{"type":"actions","elements":[{"type":"button","value":"` + strings.Repeat("v", 2001) + `","text":{"type":"plain_text","text":"b"}}]}]`,
	}
	for name, raw := range cases {
		if err := ValidateBlocks([]byte(raw), maxMessageBlocks); err == nil {
			t.Errorf("%s: validator accepted it", name)
		}
	}
	if err := ValidateBlocks([]byte("["+strings.Repeat(`{"type":"divider"},`, 50)+`{"type":"divider"}]`), maxMessageBlocks); err == nil {
		t.Error("51 blocks accepted")
	}
	if err := ValidateMessage(Message{}); err == nil {
		t.Error("message without fallback text accepted")
	}
}

func TestHelpers(t *testing.T) {
	if got := truncate("héllo wörld", 5); utf8.RuneCountInString(got) != 5 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncate = %q", got)
	}
	chunks := splitText(strings.Repeat("ab\n", 2000), 100)
	for _, c := range chunks {
		if utf8.RuneCountInString(c) > 100 {
			t.Fatal("chunk too long")
		}
	}
	if strings.Join(chunks, "") != strings.Repeat("ab\n", 2000) {
		t.Fatal("splitText lost text")
	}
	if safeURL("javascript:alert(1)") != "" || safeURL("https://ok.example/x") == "" {
		t.Fatal("safeURL")
	}
	if tg, err := DecodeTarget(Target{RunID: "r", CandidateID: "c"}.Encode()); err != nil || tg.CandidateID != "c" {
		t.Fatal("target roundtrip")
	}
	if _, err := DecodeTarget("not json"); err == nil {
		t.Fatal("bad target accepted")
	}
	if _, err := DecodeTarget("{}"); err == nil {
		t.Fatal("target without ids accepted")
	}
	if (Directory{}).display(Recipient{PersonID: "0b0e0000-0000-4000-8000-000000000018"}) != "person 0b0e0000" {
		t.Fatal("unknown person display")
	}
}

func TestJudgmentWhenHumanChoseGhostsPreference(t *testing.T) {
	f := NewFixture()
	inf := f.Inference
	inf.Agreement, inf.HumanChoice = AgreementAgreed, inf.AgentPreference
	raw := mustJSON(RenderJudgment(inf, f.Strategies, FixtureRunID))
	if !strings.Contains(raw, "confirms rather than contradicts") || strings.Contains(raw, "Ghost originally preferred") {
		t.Fatal("agreement case must say the choice confirms Ghost's preference")
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func transitionOf(touched bool) *Transition {
	to := "EXPANSION"
	return &Transition{StateTransitionID: "0e5a0000-0000-4000-8000-000000000002", Status: "CANDIDATE", FromState: "REORG", ToStateCandidate: &to,
		TouchedByEvent: touched, MissingFacts: []MissingFact{
			{Key: "owner_stabilized", Description: "A champion who took over after the reorg is active.", Required: true},
			{Key: "economic_buyer_known", Description: "The economic buyer is known.", Required: false}}}
}

// Message 1 shows the structured transition: status, from -> to, and the missing facts.
func TestBIRendersTheTransitionWithItsMissingFacts(t *testing.T) {
	f := NewFixture()
	f.BI.Transition = transitionOf(true)
	m := RenderBI(f.BI, f.AccountName, "")
	raw := mustJSON(m)
	for _, want := range []string{"ghost.bi.transition", "Relationship transition", "CANDIDATE", "REORG", "EXPANSION", "Owner stabilized", "required",
		"A champion who took over after the reorg is active.", "Economic buyer known", "optional"} {
		if !strings.Contains(raw, want) {
			t.Errorf("Message 1 lacks %q:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "unchanged") {
		t.Error("a transition the event touched must not be labelled unchanged")
	}
	if err := ValidateMessage(m); err != nil {
		t.Fatal(err)
	}
}

// An open transition Event N did not touch is still shown, labelled unchanged.
func TestBILabelsAnUntouchedOpenTransitionUnchanged(t *testing.T) {
	f := NewFixture()
	f.BI.Transition = transitionOf(false)
	raw := mustJSON(RenderBI(f.BI, f.AccountName, ""))
	if !strings.Contains(raw, "unchanged by this event") || !strings.Contains(raw, "CANDIDATE") {
		t.Fatalf("an untouched open transition must be shown as unchanged:\n%s", raw)
	}
}

func TestBIWithoutATransitionShowsNone(t *testing.T) {
	f := NewFixture()
	f.BI.Transition = nil
	m := RenderBI(f.BI, f.AccountName, "")
	raw := mustJSON(m)
	if !strings.Contains(raw, "No open relationship transition") {
		t.Fatalf("Message 1 must say there is no transition:\n%s", raw)
	}
	if err := ValidateMessage(m); err != nil {
		t.Fatal(err)
	}
}

func TestBITransitionWithManyMissingFactsStaysWithinSlackLimits(t *testing.T) {
	f := NewFixture()
	tr := transitionOf(true)
	for i := 0; i < 40; i++ {
		tr.MissingFacts = append(tr.MissingFacts, MissingFact{Key: "fact_" + strings.Repeat("x", 20), Description: strings.Repeat("d", 300), Required: i%2 == 0})
	}
	f.BI.Transition = tr
	if err := ValidateMessage(RenderBI(f.BI, f.AccountName, "")); err != nil {
		t.Fatal(err)
	}
}
