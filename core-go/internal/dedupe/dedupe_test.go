package dedupe

import (
	"regexp"
	"strings"
	"testing"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestIdempotencyKeyKnownVectors(t *testing.T) {
	// Vectors computed independently with Python:
	//   hashlib.sha256(f"{len(s)}:{s}|{len(o)}:{o}|{len(e)}:{e}".encode()).hexdigest()
	cases := []struct {
		name, system, object, event, want string
	}{
		{"plain", "email", "m1", "received", "840a68878332a396227b302e13c118258e844a3f10e83156224af159e0e2f35d"},
		{"delimiter in system", "a|b", "c", "d", "0d40833dda403aeaac7a4fe6b389a5b75fd2679fa58ea2a748d1e9d8efe0adb4"},
		{"delimiter in object", "a", "b|c", "d", "ffb5aa20e45be5ec8b7ee3582a2aef6ec37133512f67be974a3e206baab17e21"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IdempotencyKey(tc.system, tc.object, tc.event); got != tc.want {
				t.Fatalf("IdempotencyKey = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestIdempotencyKeyIsAmbiguityFree(t *testing.T) {
	// A naive "a|b|c" join would collide for these two triples.
	left := IdempotencyKey("a|b", "c", "d")
	right := IdempotencyKey("a", "b|c", "d")
	if left == right {
		t.Fatalf("length prefixing failed: both %s", left)
	}
	// Embedded length markers must not collide either.
	if IdempotencyKey("a", "1:b", "c") == IdempotencyKey("a", "1", "b|1:c") {
		t.Fatal("embedded length markers collide")
	}
}

func TestIdempotencyKeyProperties(t *testing.T) {
	cases := []struct {
		name                  string
		system, object, event string
	}{
		{"empty parts", "", "", ""},
		{"unicode", "slack", "#deal-café:1700000000.000100", "posted \U0001F680"},
		{"sql chars", "crm", "x'; DROP TABLE a;--", "field:Name:O'Brien"},
		{"very long", "email", strings.Repeat("x", 100_000), "received"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := IdempotencyKey(tc.system, tc.object, tc.event)
			if !hex64.MatchString(first) {
				t.Fatalf("not 64 lowercase hex chars: %q", first)
			}
			if second := IdempotencyKey(tc.system, tc.object, tc.event); second != first {
				t.Fatal("not deterministic")
			}
		})
	}
	if IdempotencyKey("email", "m1", "received") == IdempotencyKey("email", "m1", "sent") {
		t.Fatal("event key ignored")
	}
}
