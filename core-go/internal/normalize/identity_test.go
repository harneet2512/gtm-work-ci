package normalize

import "testing"

func TestNormalizeEmailAddress(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"Marco.Ruiz@Acme.COM", "marco.ruiz@acme.com", true},
		{"  dana@vendor.example ", "dana@vendor.example", true},
		{"a+tag@sub.example.co.uk", "a+tag@sub.example.co.uk", true},
		{"", "", false},
		{"no-at-sign", "", false},
		{"@acme.com", "", false},
		{"marco@", "", false},
		{"a@b@c.com", "", false},
		{"with space@acme.com", "", false},
		{"marco@acme", "", false},
		{"marco@.acme.com", "", false},
		{"marco@acme..com", "", false},
		{"Marco@Acme.com.", "marco@acme.com", true},
		{"a@BÜCHER.Example", "a@xn--bcher-kva.example", true},
		{"a@xn--bcher-kva.example", "a@xn--bcher-kva.example", true},
		{"a@-bad-.com", "", false},
		{"a@acme.com..", "", false},
	}
	for _, tc := range cases {
		got, ok := normalizeEmailAddress(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("normalizeEmailAddress(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestIsInternalDomain(t *testing.T) {
	cases := map[string]bool{
		"vendor.example":         true,
		"Vendor.Example":         true,
		"mail.vendor.example":    true,
		"notvendor.example":      false,
		"vendor.example.evil.io": false,
		"acme.com":               false,
		"":                       false,
	}
	for domain, want := range cases {
		if got := isInternalDomain(domain); got != want {
			t.Errorf("isInternalDomain(%q) = %v, want %v", domain, got, want)
		}
	}
}

func TestFirstExternalDomain(t *testing.T) {
	if got := firstExternalDomain("a@vendor.example", "b@acme.com", "c@beta.io"); got != "acme.com" {
		t.Errorf("got %q", got)
	}
	if got := firstExternalDomain("a@vendor.example", "not-an-email"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := firstExternalDomain(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestCollapseAndTruncate(t *testing.T) {
	if got := collapseSpace("  a \n\t b   c "); got != "a b c" {
		t.Errorf("collapseSpace = %q", got)
	}
	if got := truncateRunes("héllo wörld", 5); got != "héll…" {
		t.Errorf("truncateRunes = %q", got)
	}
	if got := truncateRunes("short", 10); got != "short" {
		t.Errorf("truncateRunes short = %q", got)
	}
	if got := truncateRunes("abc", 0); got != "" {
		t.Errorf("truncateRunes zero = %q", got)
	}
}

func TestAddParticipantDeduplicatesOnIdentityAndRole(t *testing.T) {
	var list []Participant
	list = addParticipant(list, Participant{RawIdentity: "a@acme.com", Role: "to"})
	list = addParticipant(list, Participant{RawIdentity: "a@acme.com", DisplayName: "A", Role: "to"})
	list = addParticipant(list, Participant{RawIdentity: "a@acme.com", Role: "cc"})
	if len(list) != 2 {
		t.Fatalf("got %d participants, want 2: %+v", len(list), list)
	}
	if list[0].DisplayName != "A" {
		t.Errorf("first occurrence should adopt a later display name, got %+v", list[0])
	}
}
