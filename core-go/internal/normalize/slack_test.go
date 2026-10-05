package normalize

import (
	"fmt"
	"testing"
	"time"
)

func slackJSON(channel, ts, text, extra string) string {
	return fmt.Sprintf(`{"kind":"slack_message","channel":%q,"ts":%q,"user":"U02DANA","text":%q%s}`, channel, ts, text, extra)
}

func TestNormalizeSlack(t *testing.T) {
	actor := []Participant{part("slack:U02DANA", "", "actor")}
	runCases(t, []normCase{
		{
			name: "message -> SlackMessage at ts",
			ev:   event(t, "slack", "#deal-acme:1759329720.000200", "posted", "", slackJSON("#deal-acme", "1759329720.000200", "Priya is the champion.", "")),
			want: want{
				typ: "SlackMessage", occurred: "2025-10-01T14:42:00.0002Z", participants: actor,
				accountKind: HintSlack, accountHint: "#deal-acme",
				summary: "#deal-acme: Priya is the champion.",
				body:    "Priya is the champion.",
			},
		},
		{
			name: "is_decision -> SlackDecision",
			ev:   event(t, "slack", "#deal-acme:1759329720.5", "posted", "", slackJSON("#deal-acme", "1759329720.5", "Decision: send the SOC2 pack.", `,"is_decision":true,"user_email":"dana@vendor.example"`)),
			want: want{
				typ: "SlackDecision", occurred: "2025-10-01T14:42:00.5Z", participants: actor,
				accountKind: HintSlack, accountHint: "#deal-acme",
				summary: "Decision in #deal-acme: Decision: send the SOC2 pack.",
				body:    "Decision: send the SOC2 pack.",
			},
		},
		{
			name: "non-deal channel yields no account hint",
			ev:   event(t, "slack", "#random:1759329720.1", "posted", "", slackJSON("#random", "1759329720.1", "lunch?", "")),
			want: want{typ: "SlackMessage", occurred: "2025-10-01T14:42:00.1Z", participants: actor, body: "lunch?"},
		},
		{
			name: "long text is truncated in the summary but not in the body",
			ev:   event(t, "slack", "#deal-acme:1759329720.1", "posted", "", slackJSON("#deal-acme", "1759329720.1", longText(), "")),
			want: want{typ: "SlackMessage", occurred: "2025-10-01T14:42:00.1Z", participants: actor, accountKind: HintSlack, accountHint: "#deal-acme", body: longText()},
		},
	})
}

func longText() string {
	out := ""
	for i := 0; i < 400; i++ {
		out += "é"
	}
	return out
}

func TestSlackSummaryIsBounded(t *testing.T) {
	act, err := Normalize(event(t, "slack", "#deal-acme:1759329720.1", "posted", "", slackJSON("#deal-acme", "1759329720.1", longText(), "")))
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(act.Summary())); n > MaxSummaryRunes {
		t.Fatalf("summary has %d runes, limit %d", n, MaxSummaryRunes)
	}
}

func TestParseSlackTS(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"1759329720.000200", time.Unix(1759329720, 200_000).UTC(), false},
		{"1759329720.5", time.Unix(1759329720, 500_000_000).UTC(), false},
		{"1759329720.123456789123", time.Unix(1759329720, 123_456_789).UTC(), false},
		{"0.0", time.Unix(0, 0).UTC(), false},
		{"1759329720", time.Time{}, true},
		{"abc.def", time.Time{}, true},
		{".5", time.Time{}, true},
		{"99999999999999999999.1", time.Time{}, true},
		{"", time.Time{}, true},
	}
	for _, tc := range cases {
		got, err := parseSlackTS(tc.in)
		if (err != nil) != tc.wantErr || (!tc.wantErr && !got.Equal(tc.want)) {
			t.Errorf("parseSlackTS(%q) = %v, %v; want %v (err=%v)", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestNormalizeSlackRejectsInvalidInput(t *testing.T) {
	good := slackJSON("#deal-acme", "1759329720.1", "hi", "")
	runCases(t, []normCase{
		{name: "key other than posted", ev: event(t, "slack", "#deal-acme:1759329720.1", "edited", "", good), errCode: CodeUnsupportedEvent},
		{name: "object id is not channel:ts", ev: event(t, "slack", "1759329720.1", "posted", "", good), errCode: CodeInvalidEvent},
		{name: "bad ts", ev: event(t, "slack", "#deal-acme:nope", "posted", "", slackJSON("#deal-acme", "nope", "hi", "")), errCode: CodeInvalidEvent},
		{name: "empty text", ev: event(t, "slack", "#deal-acme:1759329720.1", "posted", "", slackJSON("#deal-acme", "1759329720.1", "", "")), errCode: CodeInvalidEvent},
		{name: "missing user", ev: event(t, "slack", "#c:1.1", "posted", "", `{"kind":"slack_message","channel":"#c","ts":"1.1","text":"x"}`), errCode: CodeInvalidEvent},
		{name: "bad user_email", ev: event(t, "slack", "#deal-acme:1759329720.1", "posted", "", slackJSON("#deal-acme", "1759329720.1", "hi", `,"user_email":"nope"`)), errCode: CodeInvalidEvent},
		{name: "wrong payload kind", ev: event(t, "slack", "#deal-acme:1759329720.1", "posted", "", `{"kind":"email"}`), errCode: CodeInvalidEvent},
	})
}
