package payloadhash

import (
	"strings"
	"testing"
)

func TestKeyOrderAndWhitespaceDoNotChangeTheDigest(t *testing.T) {
	a, err := SHA256([]byte(`{"b": 1, "a": {"y": [1, 2], "x": "v"}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := SHA256([]byte(`{"a":{"x":"v","y":[1,2]},"b":1}`))
	if err != nil || a != b {
		t.Fatalf("%s vs %s, %v", a, b, err)
	}
	if len(a) != 64 || strings.ToLower(a) != a {
		t.Fatalf("not lowercase sha256 hex: %q", a)
	}
}

func TestAChangedValueChangesTheDigest(t *testing.T) {
	a, _ := SHA256([]byte(`{"body":"need the SOC2 report"}`))
	b, _ := SHA256([]byte(`{"body":"need the SOC2 report."}`))
	if a == b {
		t.Fatal("different payloads share a digest")
	}
}

func TestNumbersAndHTMLCharactersAreHashedAsWritten(t *testing.T) {
	a, _ := SHA256([]byte(`{"n": 1.50, "s": "a<b&c"}`))
	b, _ := SHA256([]byte(`{"n": 1.5, "s": "a<b&c"}`))
	if a == b {
		t.Fatal("numbers are hashed as written, so 1.50 and 1.5 differ")
	}
	c, _ := SHA256([]byte(`{"s":"a\u003cb\u0026c","n":1.50}`))
	if a != c {
		t.Fatal("an escaped < or & is the same string as a literal one")
	}
}

func TestInvalidOrEmptyJSONIsRefused(t *testing.T) {
	for _, in := range []string{``, `{`, `nope`, `{} {}`} {
		if _, err := SHA256([]byte(in)); err == nil {
			t.Errorf("%q: want an error", in)
		}
	}
}
