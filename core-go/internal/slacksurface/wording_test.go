package slacksurface

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The embedded eval wording must be contracts/evals/eval_wording.json byte for byte, the same table the web's
// vocabulary.ts mirrors, so Slack and web say the same things (eval design spec 4b-3).
func TestEmbeddedWordingIsTheContract(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "evals", "eval_wording.json"))
	if err != nil {
		t.Fatal(err)
	}
	norm := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
	if !bytes.Equal(norm(wordingJSON), norm(want)) {
		t.Fatal("eval_wording.json differs from contracts/evals/eval_wording.json; copy it again")
	}
}

func TestWordingNamesVerdictsAndEvals(t *testing.T) {
	cases := []struct{ got, want string }{
		{evalName("cta_calibration"), "CTA calibration"},
		{evalName("brand_new_eval"), "Brand new eval"},
		{verdictLabel(VerdictFail), "Fail"},
		{verdictLabel(VerdictAbstain), "Unsure"},
		{verdictLabel("not_checked"), "Not checked"},
		{verdictEmoji(VerdictPass), ":white_check_mark:"},
		{verdictEmoji(VerdictNotRelevant), ":heavy_minus_sign:"},
		{evidenceClassTag("deal_data"), "Deal evidence"},
		{evidenceClassTag("unknown_class"), ""},
		{phrase("send_blocked", map[string]string{"eval": "Pricing policy"}), "Send is blocked: Pricing policy still fails."},
		{phrase("send_blocked", nil), "Send is blocked: {eval} still fails."},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if verdictOrder(VerdictFail) >= verdictOrder(VerdictWarn) || verdictOrder(VerdictWarn) >= verdictOrder(VerdictPass) {
		t.Fatal("worst first: fail < warn < pass")
	}
}

func TestFirstSentence(t *testing.T) {
	cases := map[string]string{
		"One. Two.":                       "One.",
		"No stop at the end":              "No stop at the end",
		"Costs $24,679.53 in total. More": "Costs $24,679.53 in total.",
		"":                                "",
	}
	for in, want := range cases {
		if got := firstSentence(in); got != want {
			t.Errorf("firstSentence(%q) = %q, want %q", in, got, want)
		}
	}
	if got := firstSentence(strings.Repeat("a", 400)); len([]rune(got)) > reasonChars {
		t.Fatalf("first sentence not capped: %d", len(got))
	}
}
