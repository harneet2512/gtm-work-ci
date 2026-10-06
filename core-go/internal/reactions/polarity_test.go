package reactions

import "testing"

// A reply is not automatically positive (HAR-97): the bare reply is neutral and the content decides. A reply
// that carries an objection is negative through its objection_raised row; one that moves the deal forward is
// positive through conversation_advanced.
func TestAReplyIsNeutralAndItsContentDecidesPolarity(t *testing.T) {
	if polarityOf["replied"] != "neutral" {
		t.Fatalf("replied polarity = %q, want neutral", polarityOf["replied"])
	}
	cases := map[string]string{
		"We are concerned about the budget and might go with a competitor.": "negative",
		"Sounds good, let's book a call to talk next steps.":                "positive",
		"Thanks.": "",
	}
	for text, want := range cases {
		got := ""
		for _, rule := range phraseRules {
			if rule.re.MatchString(text) {
				got = polarityOf[rule.typ]
				break
			}
		}
		if got != want {
			t.Errorf("%q: content polarity = %q, want %q", text, got, want)
		}
	}
}
