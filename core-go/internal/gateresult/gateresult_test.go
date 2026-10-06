package gateresult

import "testing"

func TestControlEffect(t *testing.T) {
	cases := []struct {
		name       string
		gate       string
		blocksSend bool
		verdict    string
		want       string
	}{
		{"a send-blocking deterministic fail refuses", "D8", true, "fail", EffectBlock},
		{"a send-blocking pass continues", "D8", true, "pass", EffectContinue},
		{"a non-blocking D8 fail only records", "D8", false, "fail", EffectRecordOnly},
		{"S2 warn marks the capability unknown", "S2", false, "warn", EffectMarkUnknown},
		{"unknown marks unknown", "B5", false, "unknown", EffectMarkUnknown},
		{"B1 fail only records", "B1", false, "fail", EffectRecordOnly},
	}
	for _, c := range cases {
		if got := ControlEffect(c.gate, c.blocksSend, c.verdict); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestEvaluatorVersion(t *testing.T) {
	if got := EvaluatorVersion("B2", "deterministic", ""); got != "B2:deterministic:v1" {
		t.Errorf("deterministic = %s", got)
	}
	if got := EvaluatorVersion("D2", "model", "d2.v3"); got != "d2.v3" {
		t.Errorf("model = %s", got)
	}
	if got := EvaluatorVersion("D2", "model", ""); got != "D2:model:unversioned" {
		t.Errorf("unversioned model = %s", got)
	}
}

func TestHumanLabel(t *testing.T) {
	if got := HumanLabel("cta_strength"); got != "Cta strength" {
		t.Errorf("label = %q", got)
	}
	if got := HumanLabel("  "); got != "" {
		t.Errorf("blank = %q", got)
	}
}
