package ingest

import "testing"

func TestMappingKey(t *testing.T) {
	cases := []struct {
		raw        string
		wantSystem string
		wantKey    string
	}{
		{"marco.ruiz@acme.com", "email", "marco.ruiz@acme.com"},
		{"crm:contact:817", "crm", "contact:817"},
		{"call:C19:speaker_02", "call", "C19:speaker_02"},
		{"slack:U02DANA", "slack", "U02DANA"},
		{"integration:enrich", "", ""},
		{"crm:", "", ""},
		{"plain-text", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		system, key := MappingKey(tc.raw)
		if system != tc.wantSystem || key != tc.wantKey {
			t.Errorf("MappingKey(%q) = (%q, %q), want (%q, %q)", tc.raw, system, key, tc.wantSystem, tc.wantKey)
		}
	}
}
