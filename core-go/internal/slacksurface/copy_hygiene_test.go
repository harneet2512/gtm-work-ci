package slacksurface

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// HAR-129 demo boundary, Slack copy hygiene: Cliff speaks to a person, so no visible text carries a raw id, a
// snake_case field name, an action id or the name "Ghost" (the speaker is Cliff).

var (
	rawUUID      = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	rawShortID   = regexp.MustCompile(`\b[0-9a-f]{8}\b`)
	snakeCase    = regexp.MustCompile(`\b[a-z]+(?:_[a-z]+)+\b`)
	actionID     = regexp.MustCompile(`\bghost\.[a-z_.]+`)
	ghostSpeaker = regexp.MustCompile(`\bGhost\b`)
)

// visibleTexts collects what a person reads in a message or modal: every "text" and "placeholder" value of
// its Block Kit JSON.
func visibleTexts(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(any)
	walk = func(n any) {
		switch x := n.(type) {
		case map[string]any:
			for k, c := range x {
				if s, ok := c.(string); ok && (k == "text" || k == "placeholder") {
					out = append(out, s)
					continue
				}
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(doc)
	return out
}

var emojiCode = regexp.MustCompile(`:[a-z0-9_+-]+:`)

func checkCopy(t *testing.T, where, text string) {
	t.Helper()
	text = emojiCode.ReplaceAllString(text, "")
	for name, re := range map[string]*regexp.Regexp{"a raw id": rawUUID, "a short id": rawShortID, "a snake_case name": snakeCase,
		"an action id": actionID, "the name Ghost": ghostSpeaker} {
		if m := re.FindString(text); m != "" {
			t.Errorf("%s shows %s (%q) in %q", where, name, m, text)
		}
	}
}

func TestUserFacingMessagesCarryNoRawIdsOrFieldNames(t *testing.T) {
	previews, err := Previews(NewFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range previews {
		for _, s := range visibleTexts(t, p.Payload) {
			checkCopy(t, p.Name, s)
		}
	}
}

func TestUnknownReferencesReadAsPlainWords(t *testing.T) {
	if got := evidenceLine(EvidenceRef{ActivityID: "0b0e0000-0000-4000-8000-000000000018"}); strings.Contains(got, "0b0e0000") {
		t.Errorf("an evidence ref without a quote shows its id: %q", got)
	}
	if got := (Directory{}).display(Recipient{PersonID: "0b0e0000-0000-4000-8000-000000000018"}); strings.Contains(got, "0b0e0000") {
		t.Errorf("an unknown recipient shows its id: %q", got)
	}
	got := knowledgeLine([]string{"0c17c000-0000-4000-8000-000000000017", "0c17c000-0000-4000-8000-000000000018"})
	if strings.Contains(got, "0c17c000") || !strings.Contains(got, "2") {
		t.Errorf("knowledge is counted, not identified: %q", got)
	}
	c := NewFixture().Strategies.StrategySet.Candidates[0]
	if len(c.StateRefs) == 0 {
		t.Fatal("the fixture candidate has no state refs; the check below would prove nothing")
	}
	for _, b := range whyBlocks("x", c) {
		for _, s := range visibleTexts(t, b) {
			checkCopy(t, "whyBlocks", s)
		}
	}
}

func TestMessage3OffersExactlyTheThreeJudgmentButtons(t *testing.T) {
	f := NewFixture()
	for _, web := range []string{testWeb, ""} {
		m := RenderJudgment(f.Inference, f.Strategies, FixtureRunID, web)
		if got := strings.Join(labelsOf(buttonsOf(m)), "|"); got != "Correct|Edit interpretation|Don't learn this" {
			t.Errorf("web=%q: buttons = %s", web, got)
		}
		if strings.Contains(mustJSON(m), "View evals") {
			t.Errorf("web=%q: Message 3 has no View evals", web)
		}
	}
	for _, inf := range []JudgmentInference{f.Confirmed, f.Corrected, f.NoLearning} {
		if got := labelsOf(buttonsOf(RenderJudgment(inf, f.Strategies, FixtureRunID, testWeb))); len(got) != 0 {
			t.Errorf("an answered Message 3 keeps buttons: %v", got)
		}
	}
}

func TestUserErrorsSpeakAsCliffAndNameNoActionId(t *testing.T) {
	const action = "ghost.selected.send"
	for _, err := range []error{errBusy, errNotSent, errUnknown, ErrNotFound, errEditAfterSend, errVerdictLocked,
		slackSideError{errors.New("slack")}, errors.New("boom")} {
		checkCopy(t, "userMessage", userMessage(action, err))
	}
	for _, err := range []error{errBusy, errEditAfterSend, &AmbiguousError{Err: errors.New("t")}, errors.New("boom")} {
		_, msg := modalError(err, "b")
		checkCopy(t, "modalError", msg)
	}
	tgt := Target{RunID: FixtureRunID}
	for _, v := range []any{LoadingModal(tgt), ErrorModal(tgt)} {
		for _, s := range visibleTexts(t, v) {
			checkCopy(t, "modal", s)
		}
	}
}

// TestSlackSourceNeverNamesGhostToAPerson scans the string literals of the Slack copy files.
func TestSlackSourceNeverNamesGhostToAPerson(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range []string{"handler.go", "handler_views.go", "handler_actions.go", "render_modals.go", "render_evals.go",
		"render_common.go", "render_selected.go", "render_judgment.go"} {
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if _, ok := n.(*ast.ImportSpec); ok {
				return false
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, _ := strconv.Unquote(lit.Value); ghostSpeaker.MatchString(s) {
				t.Errorf("%s: %q speaks as Ghost; the speaker is Cliff", fset.Position(lit.Pos()), s)
			}
			return true
		})
	}
}

func TestSlackContractHasNoDryRunStatus(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "slack", "actions.md"))
	if err != nil {
		t.Skip("Markdown contracts are stripped from the CI mirror")
	}
	if strings.Contains(strings.ToLower(string(raw)), "dry run") {
		t.Error("contracts/slack/actions.md must not describe a Recorded (dry run) state")
	}
}
