package normalize

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// WP32 (HAR-131): the envelope's origin/provenance pair follows source_event.v1.json.
func TestNormalizeOriginValidation(t *testing.T) {
	good := emailJSON("inbound", "marco@acme.com", []string{"dana@ghostvendor.com"}, nil, "", "")
	with := func(origin, provenance string) SourceEvent {
		ev := event(t, "email", "m1", "received", "", good)
		ev.Origin, ev.Provenance = origin, provenance
		return ev
	}
	runCases(t, []normCase{
		{name: "unknown origin", ev: with("imagined", ""), errCode: CodeInvalidEvent},
		{name: "synthetic without provenance", ev: with("synthetic", ""), errCode: CodeInvalidEvent},
		{name: "dataset without provenance", ev: with("dataset", ""), errCode: CodeInvalidEvent},
		{name: "provenance without origin", ev: with("", "synthetic:v1"), errCode: CodeInvalidEvent},
		{name: "live with provenance", ev: with("live", "crmarena-pro:b2b"), errCode: CodeInvalidEvent},
		{name: "synthetic with dataset provenance", ev: with("synthetic", "crmarena-pro:b2b"), errCode: CodeInvalidEvent},
		{name: "unversioned synthetic layer", ev: with("synthetic", "synthetic:latest"), errCode: CodeInvalidEvent},
		{name: "dataset with synthetic provenance", ev: with("dataset", "synthetic:v1"), errCode: CodeInvalidEvent},
		{name: "malformed provenance", ev: with("dataset", "CRMArena Pro"), errCode: CodeInvalidEvent},
	})
	for _, ok := range []SourceEvent{with("synthetic", "synthetic:v1"), with("dataset", "crmarena-pro:b2b"), with("live", ""), with("", "")} {
		if _, err := Normalize(ok); err != nil {
			t.Errorf("origin %q provenance %q rejected: %v", ok.Origin, ok.Provenance, err)
		}
	}
}

func TestOriginDoesNotChangeTheActivity(t *testing.T) {
	good := emailJSON("inbound", "marco@acme.com", []string{"dana@ghostvendor.com"}, nil, "", "")
	plain := event(t, "email", "m1", "received", "", good)
	marked := plain
	marked.Origin, marked.Provenance = "synthetic", "synthetic:v1"
	a, errA := Normalize(plain)
	b, errB := Normalize(marked)
	if errA != nil || errB != nil {
		t.Fatalf("normalize: %v / %v", errA, errB)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the origin marker must not reach the normalized activity")
	}
}

func TestSourceEventOriginJSONRoundTrip(t *testing.T) {
	in := []byte(`{"source_system":"email","source_object_id":"m1","source_event_key":"received","origin":"synthetic","provenance":"synthetic:v1","payload":{}}`)
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.DisallowUnknownFields()
	var ev SourceEvent
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	if ev.Origin != "synthetic" || ev.Provenance != "synthetic:v1" {
		t.Fatalf("decoded %+v", ev)
	}
	out, _ := json.Marshal(SourceEvent{SourceSystem: "email", SourceObjectID: "m1", SourceEventKey: "k", Payload: json.RawMessage(`{}`)})
	if bytes.Contains(out, []byte("origin")) || bytes.Contains(out, []byte("provenance")) {
		t.Fatalf("empty markers must be omitted: %s", out)
	}
}
