package evalinput

import (
	"regexp"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

func TestStateDigestIsStableAndSeesAnyChange(t *testing.T) {
	a := reducer.AccountState{AccountID: "0a0c0000-0000-4000-8000-000000000001", Version: 7}
	first, err := StateDigest(a)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("digest %q is not a sha-256", first)
	}
	if again, _ := StateDigest(a); again != first {
		t.Error("the digest of the same state changed")
	}
	b := a
	b.CoverageGaps = []string{"economic_buyer"}
	if other, _ := StateDigest(b); other == first {
		t.Error("a state with a different document has the same digest")
	}
	c := a
	c.Version = 8
	if other, _ := StateDigest(c); other == first {
		t.Error("a state with a different version has the same digest")
	}
}
