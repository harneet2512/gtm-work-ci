package normalize

import (
	"fmt"
	"testing"
)

// emailJSON renders an email payload; extra is spliced in before the closing brace.
func emailJSON(direction, from string, to, cc []string, inReplyTo, extra string) string {
	addrs := func(list []string) string {
		out := ""
		for i, a := range list {
			if i > 0 {
				out += ","
			}
			out += fmt.Sprintf(`{"email":%q}`, a)
		}
		return "[" + out + "]"
	}
	return fmt.Sprintf(`{"kind":"email","message_id":"m1","thread_id":"thr-1","direction":%q,
		"from":{"email":%q,"name":"Sender Name"},"to":%s,"cc":%s,%s
		"date":"2026-09-29T15:42:00+02:00","subject":"  Re: Plan\n for   EU ","body_text":"Hello\nWorld"%s}`,
		direction, from, addrs(to), addrs(cc), inReplyTo, extra)
}

func TestNormalizeEmail(t *testing.T) {
	const at = "2026-09-29T13:42:00Z" // payload date converted to UTC
	replied := `"in_reply_to":"<prev@x>",`
	runCases(t, []normCase{
		{
			name: "inbound without in_reply_to is EmailReceived",
			ev:   event(t, "email", "m1", "received", "", emailJSON("inbound", "Marco.Ruiz@Acme.com", []string{"dana@vendor.example"}, []string{"priya@acme.com"}, "", "")),
			want: want{
				typ: "EmailReceived", occurred: at,
				participants: []Participant{
					part("marco.ruiz@acme.com", "Sender Name", "from"),
					part("dana@vendor.example", "", "to"),
					part("priya@acme.com", "", "cc"),
				},
				accountHint: "acme.com", oppHint: "thr-1", oppKind: HintEmailThread,
				summary: "Email from marco.ruiz@acme.com: Re: Plan for EU",
				body:    "Hello\nWorld",
			},
		},
		{
			name: "inbound with in_reply_to is EmailReply and prefers crm_opportunity_ref",
			ev: event(t, "email", "m1", "received", "", emailJSON("inbound", "marco@acme.com", []string{"dana@vendor.example"}, nil, replied,
				`,"crm_opportunity_ref":"opp:AC-4-EXP"`)),
			want: want{
				typ: "EmailReply", occurred: at,
				participants: []Participant{
					part("marco@acme.com", "Sender Name", "from"),
					part("dana@vendor.example", "", "to"),
				},
				accountHint: "acme.com", oppHint: "opp:AC-4-EXP", moreOpp: []Hint{{Kind: HintEmailThread, Value: "thr-1"}},
				summary: "Reply from marco@acme.com: Re: Plan for EU",
				body:    "Hello\nWorld",
			},
		},
		{
			name: "empty in_reply_to counts as no reply",
			ev: event(t, "email", "m1", "received", "", emailJSON("inbound", "marco@acme.com", []string{"dana@vendor.example"}, nil,
				`"in_reply_to":"",`, `,"crm_opportunity_ref":null`)),
			want: want{
				typ: "EmailReceived", occurred: at,
				participants: []Participant{
					part("marco@acme.com", "Sender Name", "from"),
					part("dana@vendor.example", "", "to"),
				},
				accountHint: "acme.com", oppHint: "thr-1", oppKind: HintEmailThread,
				body: "Hello\nWorld",
			},
		},
		{
			name: "outbound uses the first external recipient domain",
			ev:   event(t, "email", "m1", "sent", "", emailJSON("outbound", "dana@vendor.example", []string{"colleague@vendor.example", "priya@acme.com", "x@beta.io"}, nil, "", "")),
			want: want{
				typ: "EmailSent", occurred: at,
				participants: []Participant{
					part("dana@vendor.example", "Sender Name", "from"),
					part("colleague@vendor.example", "", "to"),
					part("priya@acme.com", "", "to"),
					part("x@beta.io", "", "to"),
				},
				accountHint: "acme.com", moreHints: []Hint{dom("beta.io")}, oppHint: "thr-1", oppKind: HintEmailThread,
				summary: "Email to colleague@vendor.example: Re: Plan for EU",
				body:    "Hello\nWorld",
			},
		},
		{
			name: "internal-only outbound has no account hint",
			ev:   event(t, "email", "m1", "sent", "", emailJSON("outbound", "dana@vendor.example", []string{"lee@vendor.example"}, nil, "", "")),
			want: want{
				typ: "EmailSent", occurred: at,
				participants: []Participant{
					part("dana@vendor.example", "Sender Name", "from"),
					part("lee@vendor.example", "", "to"),
				},
				oppHint: "thr-1", oppKind: HintEmailThread, body: "Hello\nWorld",
			},
		},
		{
			name: "inbound forwarded by a rep falls back to the first external cc",
			ev:   event(t, "email", "m1", "received", "", emailJSON("inbound", "dana@vendor.example", []string{"lee@vendor.example"}, []string{"Priya@ACME.com"}, "", "")),
			want: want{
				typ: "EmailReceived", occurred: at,
				participants: []Participant{
					part("dana@vendor.example", "Sender Name", "from"),
					part("lee@vendor.example", "", "to"),
					part("priya@acme.com", "", "cc"),
				},
				accountHint: "acme.com", oppHint: "thr-1", oppKind: HintEmailThread, body: "Hello\nWorld",
			},
		},
		{
			name: "an unknown sender falls back to later To/CC domains, in order, without duplicates",
			ev: event(t, "email", "m1", "received", "", emailJSON("inbound", "sam@free.example", []string{"dana@vendor.example", "a@acme.com"},
				[]string{"b@acme.com", "c@beta.io"}, "", "")),
			want: want{
				typ: "EmailReceived", occurred: at,
				participants: []Participant{
					part("sam@free.example", "Sender Name", "from"),
					part("dana@vendor.example", "", "to"),
					part("a@acme.com", "", "to"),
					part("b@acme.com", "", "cc"),
					part("c@beta.io", "", "cc"),
				},
				accountHint: "free.example", moreHints: []Hint{dom("acme.com"), dom("beta.io")},
				oppHint: "thr-1", oppKind: HintEmailThread, body: "Hello\nWorld",
			},
		},
		{
			name: "trailing dots are stripped and internationalized domains become lower-case punycode",
			ev:   event(t, "email", "m1", "received", "", emailJSON("inbound", "Marco@BÜCHER.Example.", []string{"dana@vendor.example"}, nil, "", "")),
			want: want{
				typ: "EmailReceived", occurred: at,
				participants: []Participant{
					part("marco@xn--bcher-kva.example", "Sender Name", "from"),
					part("dana@vendor.example", "", "to"),
				},
				accountHint: "xn--bcher-kva.example", oppHint: "thr-1", oppKind: HintEmailThread, body: "Hello\nWorld",
			},
		},
		{
			name: "duplicate recipients collapse to one participant row",
			ev:   event(t, "email", "m1", "received", "", emailJSON("inbound", "marco@acme.com", []string{"dana@vendor.example", "DANA@vendor.example"}, nil, "", "")),
			want: want{
				typ: "EmailReceived", occurred: at,
				participants: []Participant{
					part("marco@acme.com", "Sender Name", "from"),
					part("dana@vendor.example", "", "to"),
				},
				accountHint: "acme.com", oppHint: "thr-1", oppKind: HintEmailThread, body: "Hello\nWorld",
			},
		},
	})
}

func TestNormalizeEmailRejectsInvalidInput(t *testing.T) {
	good := emailJSON("inbound", "marco@acme.com", []string{"dana@vendor.example"}, nil, "", "")
	runCases(t, []normCase{
		{name: "received key on outbound mail", ev: event(t, "email", "m1", "received", "", emailJSON("outbound", "d@vendor.example", []string{"a@acme.com"}, nil, "", "")), errCode: CodeInvalidEvent},
		{name: "sent key on inbound mail", ev: event(t, "email", "m1", "sent", "", good), errCode: CodeInvalidEvent},
		{name: "unknown event key", ev: event(t, "email", "m1", "bounced", "", good), errCode: CodeUnsupportedEvent},
		{name: "object id differs from message_id", ev: event(t, "email", "other", "received", "", good), errCode: CodeInvalidEvent},
		{name: "wrong payload kind", ev: event(t, "email", "m1", "received", "", `{"kind":"call"}`), errCode: CodeInvalidEvent},
		{name: "missing kind", ev: event(t, "email", "m1", "received", "", `{}`), errCode: CodeInvalidEvent},
		{name: "no recipients", ev: event(t, "email", "m1", "received", "", emailJSON("inbound", "marco@acme.com", nil, nil, "", "")), errCode: CodeInvalidEvent},
		{name: "malformed sender address", ev: event(t, "email", "m1", "received", "", emailJSON("inbound", "not-an-email", []string{"dana@vendor.example"}, nil, "", "")), errCode: CodeInvalidEvent},
		{name: "unknown direction", ev: event(t, "email", "m1", "received", "", emailJSON("sideways", "marco@acme.com", []string{"dana@vendor.example"}, nil, "", "")), errCode: CodeInvalidEvent},
		{name: "unknown payload field", ev: event(t, "email", "m1", "received", "", emailJSON("inbound", "marco@acme.com", []string{"dana@vendor.example"}, nil, "", `,"surprise":1`)), errCode: CodeInvalidEvent},
		{name: "missing date", ev: event(t, "email", "m1", "received", "", `{"kind":"email","message_id":"m1","thread_id":"t","direction":"inbound","from":{"email":"a@acme.com"},"to":[{"email":"b@vendor.example"}],"subject":"s","body_text":"b"}`), errCode: CodeInvalidEvent},
		{name: "date is not a timestamp", ev: event(t, "email", "m1", "received", "", `{"kind":"email","message_id":"m1","thread_id":"t","direction":"inbound","from":{"email":"a@acme.com"},"to":[{"email":"b@vendor.example"}],"date":"yesterday","subject":"s","body_text":"b"}`), errCode: CodeInvalidEvent},
		{name: "payload is an array", ev: event(t, "email", "m1", "received", "", `[1]`), errCode: CodeInvalidEvent},
		{name: "payload has trailing data", ev: event(t, "email", "m1", "received", "", good+`{}`), errCode: CodeInvalidEvent},
	})
}
