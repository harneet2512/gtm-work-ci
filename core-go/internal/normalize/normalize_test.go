package normalize

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeEnvelopeValidation(t *testing.T) {
	good := emailJSON("inbound", "marco@acme.com", []string{"dana@ghostvendor.com"}, nil, "", "")
	long := strings.Repeat("x", 513)
	runCases(t, []normCase{
		{name: "unknown source system", ev: event(t, "fax", "m1", "received", "", good), errCode: CodeInvalidEvent},
		{name: "empty source system", ev: event(t, "", "m1", "received", "", good), errCode: CodeInvalidEvent},
		{name: "known but unmapped source system", ev: event(t, "marketing", "m1", "received", "", `{"kind":"x"}`), errCode: CodeUnsupportedEvent},
		{name: "empty object id", ev: event(t, "email", "", "received", "", good), errCode: CodeInvalidEvent},
		{name: "object id too long", ev: event(t, "email", long, "received", "", good), errCode: CodeInvalidEvent},
		{name: "empty event key", ev: event(t, "email", "m1", "", "", good), errCode: CodeInvalidEvent},
		{name: "event key too long", ev: event(t, "email", "m1", strings.Repeat("k", 257), "", good), errCode: CodeInvalidEvent},
		{name: "empty payload", ev: event(t, "email", "m1", "received", "", ``), errCode: CodeInvalidEvent},
		{name: "null payload", ev: event(t, "email", "m1", "received", "", `null`), errCode: CodeInvalidEvent},
		{name: "scalar payload", ev: event(t, "email", "m1", "received", "", `"hi"`), errCode: CodeInvalidEvent},
		{name: "truncated payload json", ev: event(t, "email", "m1", "received", "", `{"kind":`), errCode: CodeInvalidEvent},
		{name: "oversized connector", ev: func() SourceEvent {
			ev := event(t, "email", "m1", "received", "", good)
			ev.Connector = strings.Repeat("c", 129)
			return ev
		}(), errCode: CodeInvalidEvent},
		{name: "oversized connector version", ev: func() SourceEvent {
			ev := event(t, "email", "m1", "received", "", good)
			ev.ConnectorVersion = strings.Repeat("v", 65)
			return ev
		}(), errCode: CodeInvalidEvent},
		{name: "NUL escape cannot be stored in jsonb", ev: event(t, "slack", "#deal-a:1.1", "posted", "", `{"kind":"slack_message","channel":"#deal-a","ts":"1.1","user":"U1","text":"bad\u0000text"}`), errCode: CodeInvalidEvent},
	})
}

func TestNULEscapeDetectionIgnoresEscapedBackslash(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`{"a":"x\u0000y"}`, true},
		{`{"a":"x\\u0000y"}`, false}, // escaped backslash followed by the literal text u0000
		{`{"a":"x\\\u0000y"}`, true},
		{`{"a":"plain"}`, false},
	}
	for _, tc := range cases {
		if got := containsEscapedNUL([]byte(tc.in)); got != tc.want {
			t.Errorf("containsEscapedNUL(%s) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeIsDeterministicAndPure(t *testing.T) {
	ev := event(t, "email", "m1", "received", "", emailJSON("inbound", "marco@acme.com", []string{"dana@ghostvendor.com"}, nil, "", ""))
	payloadBefore := string(ev.Payload)
	first, err := Normalize(ev)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Normalize(ev)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same input produced different activities")
	}
	if string(ev.Payload) != payloadBefore {
		t.Fatal("Normalize mutated the input payload")
	}
}

func TestActivityIsImmutableThroughAccessors(t *testing.T) {
	act, err := Normalize(event(t, "email", "m1", "received", "", emailJSON("inbound", "marco@acme.com", []string{"dana@ghostvendor.com"}, nil, "", "")))
	if err != nil {
		t.Fatal(err)
	}
	parts := act.Participants()
	parts[0].RawIdentity = "tampered@evil.test"
	parts[0] = Participant{}
	if got := act.Participants()[0].RawIdentity; got != "marco@acme.com" {
		t.Fatalf("caller mutation leaked into the activity: %q", got)
	}
}

func TestIdempotencyKeyMatchesDedupePackage(t *testing.T) {
	act, err := Normalize(event(t, "email", "m1", "received", "", emailJSON("inbound", "marco@acme.com", []string{"dana@ghostvendor.com"}, nil, "", "")))
	if err != nil {
		t.Fatal(err)
	}
	if act.IdempotencyKey() != "840a68878332a396227b302e13c118258e844a3f10e83156224af159e0e2f35d" {
		t.Fatalf("key = %s", act.IdempotencyKey())
	}
}

func TestSourceEventJSONRoundTrip(t *testing.T) {
	raw := `{"source_system":"email","source_object_id":"m1","source_event_key":"received","occurred_at":"2026-09-29T15:42:00Z","connector":"gmail","connector_version":"0.1.0","payload":{"kind":"email"}}`
	var ev SourceEvent
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ev); err != nil {
		t.Fatal(err)
	}
	if ev.OccurredAt == nil || ev.Connector != "gmail" || string(ev.Payload) != `{"kind":"email"}` {
		t.Fatalf("decoded %+v", ev)
	}
}

func TestEveryContractExampleNormalizes(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "contracts", "examples")
	raw, err := os.ReadFile(filepath.Join(dir, "source_event.example.json"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	var ev SourceEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	act, err := Normalize(ev)
	if err != nil {
		t.Fatalf("contracts/examples/source_event.example.json does not normalize: %v", err)
	}
	if act.Type() != "EmailReply" || act.AccountHint() != "acme.com" || act.OpportunityHint() != "thr-acme-eu-expansion" {
		t.Fatalf("unexpected activity: %s %s %s", act.Type(), act.AccountHint(), act.OpportunityHint())
	}
}

func TestValidationErrorFormatting(t *testing.T) {
	err := invalid("bad %s", "thing")
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Code != CodeInvalidEvent || !strings.Contains(err.Error(), "bad thing") {
		t.Fatalf("invalid() = %v", err)
	}
	err = unsupported("no %s", "mapping")
	if !errors.As(err, &verr) || verr.Code != CodeUnsupportedEvent || !strings.Contains(err.Error(), "no mapping") {
		t.Fatalf("unsupported() = %v", err)
	}
	if !IsValidation(err) || IsValidation(errors.New("plain")) || IsValidation(nil) {
		t.Fatal("IsValidation misclassifies")
	}
}
