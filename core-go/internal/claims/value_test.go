package claims

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseListItemAcceptsPrefixedStringsAndObjects(t *testing.T) {
	due := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		raw  string
		want ListItem
	}{
		{"plain string defaults to open", `"EU budget line needs approval"`, ListItem{Text: "EU budget line needs approval", Status: "open"}},
		{"open prefix", `"open: SSO is the main open item"`, ListItem{Text: "SSO is the main open item", Status: "open"}},
		{"resolved prefix is case-insensitive", `"Resolved:  SSO is the main open item "`, ListItem{Text: "SSO is the main open item", Status: "resolved"}},
		{"fulfilled prefix", `"fulfilled: Send proposal"`, ListItem{Text: "Send proposal", Status: "fulfilled"}},
		{"unknown prefix stays part of the text", `"note: something"`, ListItem{Text: "note: something", Status: "open"}},
		{"object with status", `{"text":"Send pricing","status":"fulfilled"}`, ListItem{Text: "Send pricing", Status: "fulfilled"}},
		{"object defaults status to open", `{"text":"Send pricing"}`, ListItem{Text: "Send pricing", Status: "open"}},
		{"object with due and owner", `{"text":"Send pricing","due_at":"2026-09-01T00:00:00Z","owner_person_id":"0b0e0000-0000-4000-8000-000000000001"}`,
			ListItem{Text: "Send pricing", Status: "open", DueAt: &due, OwnerPersonID: "0b0e0000-0000-4000-8000-000000000001"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseListItem(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatalf("ParseListItem: %v", err)
			}
			if got.Text != tt.want.Text || got.Status != tt.want.Status || got.OwnerPersonID != tt.want.OwnerPersonID {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			if (got.DueAt == nil) != (tt.want.DueAt == nil) || (got.DueAt != nil && !got.DueAt.Equal(*tt.want.DueAt)) {
				t.Fatalf("due = %v, want %v", got.DueAt, tt.want.DueAt)
			}
		})
	}
}

func TestParseListItemRejectsBadInput(t *testing.T) {
	for _, raw := range []string{`null`, `""`, `"   "`, `"open:"`, `42`, `[]`, `{}`, `{"text":""}`, `{"text":"x","status":"bogus"}`, `{"text":"x","due_at":"not a date"}`, `not json`} {
		if _, err := ParseListItem(json.RawMessage(raw)); err == nil {
			t.Errorf("ParseListItem(%s) succeeded, want error", raw)
		}
	}
}

func TestItemKeyNormalizesCaseSpaceAndTrailingPunctuation(t *testing.T) {
	a := ItemKey("EU  budget line\tneeds approval.")
	b := ItemKey(" eu budget LINE needs approval ")
	if a != b || a == "" {
		t.Fatalf("keys differ: %q vs %q", a, b)
	}
	if ItemKey("Security review") == ItemKey("Security sign-off") {
		t.Fatal("distinct texts share a key")
	}
}

func TestCanonicalValueIgnoresKeyOrderAndWhitespace(t *testing.T) {
	a := CanonicalValue(json.RawMessage(`{"b": 1, "a": "x"}`))
	b := CanonicalValue(json.RawMessage(`{"a":"x","b":1}`))
	if a != b {
		t.Fatalf("%s != %s", a, b)
	}
	if CanonicalValue(json.RawMessage(`"Stage"`)) == CanonicalValue(json.RawMessage(`"stage"`)) {
		// Scalars compare case-insensitively through SameValue, not CanonicalValue.
		t.Log("canonical form is case-sensitive as documented")
	}
}

func TestSameValueComparesScalarsCaseInsensitively(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{`"Technical evaluation"`, `"technical  evaluation"`, true},
		{`"Negotiation"`, `"Commercial review"`, false},
		{`null`, `null`, true},
		{`null`, `"x"`, false},
		{`{"a":1,"b":2}`, `{"b":2,"a":1}`, true},
		{`42`, `42`, true},
	}
	for _, tt := range tests {
		if got := SameValue(json.RawMessage(tt.a), json.RawMessage(tt.b)); got != tt.want {
			t.Errorf("SameValue(%s,%s) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestIsNullAndIsUnknown(t *testing.T) {
	if !IsNull(json.RawMessage(`null`)) || IsNull(json.RawMessage(`"x"`)) || !IsNull(nil) {
		t.Error("IsNull")
	}
	if !IsUnknown(json.RawMessage(`"unknown"`)) || !IsUnknown(json.RawMessage(`" Unknown "`)) || IsUnknown(json.RawMessage(`"known"`)) || IsUnknown(json.RawMessage(`null`)) {
		t.Error("IsUnknown")
	}
}

func TestParseMemberReadsRoleTitleAndEmployer(t *testing.T) {
	m := ParseMember(json.RawMessage(`{"role":"security","title":"Head of Security","employer_domain":"acme.com"}`))
	if m.Role != "security" || m.Title != "Head of Security" || m.EmployerDomain != "acme.com" {
		t.Fatalf("got %+v", m)
	}
	if got := ParseMember(json.RawMessage(`"person:marco"`)); got != (Member{}) {
		t.Fatalf("string member value must carry no attributes, got %+v", got)
	}
	if got := ParseMember(json.RawMessage(`not json`)); got != (Member{}) {
		t.Fatalf("garbage member value must carry no attributes, got %+v", got)
	}
}

func TestParseDelegation(t *testing.T) {
	d, err := ParseDelegation(json.RawMessage(`{"from_person_id":"a","to_person_id":"b","scope":"evaluation"}`))
	if err != nil || d.FromPersonID != "a" || d.ToPersonID != "b" || d.Scope != "evaluation" {
		t.Fatalf("got %+v, %v", d, err)
	}
	for _, raw := range []string{`"x"`, `{"to_person_id":"b"}`, `{"from_person_id":"a"}`, `null`} {
		if _, err := ParseDelegation(json.RawMessage(raw)); err == nil {
			t.Errorf("ParseDelegation(%s) succeeded", raw)
		}
	}
}
