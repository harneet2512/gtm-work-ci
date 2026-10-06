package bucket2

import "testing"

func j(dims ...Dimension) Judged {
	return Judged{Gate: "D2", SubGate: "candidate", Type: "StrategyCandidate", ID: "c", SpanID: "candidates:s", Model: "m", PromptVersion: "v", Dimensions: dims}
}

func TestFromDimensionsFoldsHonestly(t *testing.T) {
	ev := []string{"x"}
	cases := []struct {
		name string
		in   []Dimension
		want Verdict
	}{
		{"all pass", []Dimension{{"a", "pass", "w", ev}, {"b", "pass", "w", ev}}, Pass},
		{"warn decides", []Dimension{{"a", "pass", "w", ev}, {"b", "warn", "w", ev}}, Warn},
		{"fail decides", []Dimension{{"a", "warn", "w", ev}, {"b", "fail", "w", ev}}, Fail},
		{"pass without evidence is unknown", []Dimension{{"a", "pass", "w", nil}}, Unknown},
		{"abstain only is unknown", []Dimension{{"a", "abstain", "w", ev}}, Unknown},
		{"one unknown dimension makes it unknown", []Dimension{{"a", "pass", "w", ev}, {"b", "unknown", "w", nil}}, Unknown},
		{"a warn still wins over an unknown", []Dimension{{"a", "warn", "w", ev}, {"b", "unknown", "w", nil}}, Warn},
		{"no dimensions", nil, Unknown},
	}
	for _, c := range cases {
		r := FromDimensions(j(c.in...))
		if r.Verdict != c.want {
			t.Errorf("%s = %s, want %s", c.name, r.Verdict, c.want)
		}
		if r.Verdict != Unknown && len(r.EvidenceRefs) == 0 {
			t.Errorf("%s: verdict without evidence", c.name)
		}
		if r.Grader.Kind != "model" || r.Calibrated {
			t.Errorf("%s: grader/calibration wrong: %+v", c.name, r)
		}
	}
	r := FromDimensions(j(Dimension{"a", "pass", "w", []string{"x"}}, Dimension{"b", "unknown", "w", nil}))
	if want := "1 of 2 dimension(s) could not be judged"; !contains(r.Why, want) {
		t.Fatalf("why = %q", r.Why)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestInferenceResult(t *testing.T) {
	r := InferenceResult("e", Inference{ID: "i", EditClasses: []string{"cta"}, SignalStrength: "moderate", Statement: "s"}, "m", []string{"activity:a"})
	if r.Verdict != Pass || r.Gate != "D5" || r.Grader.Kind != "model" {
		t.Fatalf("got %+v", r)
	}
	if u := InferenceResult("e", Inference{ID: "i", Unknown: true, Statement: "s"}, "m", []string{"activity:a"}); u.Verdict != Unknown {
		t.Fatalf("unknown inference must stay unknown, got %s", u.Verdict)
	}
	if n := InferenceResult("e", Inference{ID: "i", Statement: "s"}, "m", nil); n.Verdict != Unknown {
		t.Fatalf("no evidence must be unknown, got %s", n.Verdict)
	}
}
