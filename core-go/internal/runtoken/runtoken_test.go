package runtoken

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

const runA = "11111111-1111-4111-8111-111111111111"

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func signer(t *testing.T, key string) *Signer {
	t.Helper()
	s, err := NewSigner([]byte(key), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const key1 = "0123456789abcdef0123456789abcdef"

func TestIssueThenVerifyReturnsTheRun(t *testing.T) {
	s := signer(t, key1)
	tok, err := s.Issue(runA, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._~+/=-]{32,512}$`).MatchString(tok) {
		t.Fatalf("token %q is outside the worker's bearer charset", tok)
	}
	got, err := s.Verify(tok, t0.Add(30*time.Second))
	if err != nil || got != runA {
		t.Fatalf("Verify = %q, %v", got, err)
	}
}

func TestVerifyRejectsEveryBadToken(t *testing.T) {
	s := signer(t, key1)
	other := signer(t, "fedcba9876543210fedcba9876543210")
	good, _ := s.Issue(runA, t0)
	foreign, _ := other.Issue(runA, t0)
	parts := strings.Split(good, ".")
	swapped := strings.Join([]string{parts[0], "22222222-2222-4222-8222-222222222222", parts[2], parts[3]}, ".")
	extended := strings.Join([]string{parts[0], parts[1], "9999999999", parts[3]}, ".")
	cases := map[string]struct {
		token string
		now   time.Time
	}{
		"empty":            {"", t0},
		"garbage":          {"not-a-token", t0},
		"other key":        {foreign, t0},
		"other run id":     {swapped, t0},
		"extended expiry":  {extended, t0},
		"truncated sig":    {good[:len(good)-2], t0},
		"non hex sig":      {strings.Join([]string{parts[0], parts[1], parts[2], strings.Repeat("z", 64)}, "."), t0},
		"expired":          {good, t0.Add(time.Minute)},
		"expired later":    {good, t0.Add(time.Hour)},
		"too long":         {good + strings.Repeat("a", 600), t0},
		"extra segment":    {good + ".x", t0},
		"wrong prefix":     {"rt2" + good[3:], t0},
		"uppercase run id": {strings.ToUpper(good), t0},
	}
	for name, c := range cases {
		if _, err := s.Verify(c.token, c.now); err != ErrInvalid {
			t.Errorf("%s: Verify = %v, want ErrInvalid", name, err)
		}
	}
}

func TestVerifyRejectsTokensThatOutliveMaxTTLAndUppercaseSignatures(t *testing.T) {
	s := signer(t, key1)
	long, _ := NewSigner([]byte(key1), MaxTTL)
	tok, _ := long.Issue(runA, t0)
	if _, err := s.Verify(tok, t0); err != nil {
		t.Fatalf("a token of exactly MaxTTL was refused: %v", err)
	}
	if _, err := s.Verify(tok, t0.Add(-time.Minute)); err == nil {
		t.Fatal("a token expiring more than MaxTTL ahead was accepted")
	}
	good, _ := s.Issue(runA, t0)
	parts := strings.Split(good, ".")
	upper := strings.Join([]string{parts[0], parts[1], parts[2], strings.ToUpper(parts[3])}, ".")
	if _, err := s.Verify(upper, t0); err == nil {
		t.Fatal("an uppercase-hex signature was accepted as a second spelling of the token")
	}
}

func TestNewSignerAndIssueValidateArguments(t *testing.T) {
	if _, err := NewSigner([]byte("short"), time.Minute); err == nil {
		t.Error("short key accepted")
	}
	for _, ttl := range []time.Duration{0, -time.Second, MaxTTL + time.Second} {
		if _, err := NewSigner([]byte(key1), ttl); err == nil {
			t.Errorf("ttl %s accepted", ttl)
		}
	}
	s := signer(t, key1)
	for _, id := range []string{"", "x", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", runA + "."} {
		if _, err := s.Issue(id, t0); err == nil {
			t.Errorf("run id %q accepted", id)
		}
	}
}

func TestDeriveKeyIsDeterministicAndSeparated(t *testing.T) {
	a, b := DeriveKey("api-token"), DeriveKey("api-token")
	if string(a) != string(b) || len(a) < MinKeyBytes {
		t.Fatalf("derived key not stable or too short (%d)", len(a))
	}
	if string(a) == "api-token" || string(DeriveKey("other")) == string(a) {
		t.Fatal("derived key equals its input or collides")
	}
}

func TestSignerCopiesItsKey(t *testing.T) {
	key := []byte(key1)
	s, _ := NewSigner(key, time.Minute)
	tok, _ := s.Issue(runA, t0)
	key[0] ^= 0xff
	if _, err := s.Verify(tok, t0); err != nil {
		t.Fatalf("mutating the caller's key broke the signer: %v", err)
	}
}
