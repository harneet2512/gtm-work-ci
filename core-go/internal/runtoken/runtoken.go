// Package runtoken mints and verifies the run-scoped bearer tokens of GET /internal/ctx/{tool}
// (contracts/openapi/core.yaml). A token names exactly one agent run and carries its own expiry;
// core derives the run, and through it the account, from the token alone, so a draft agent has no
// way to ask for another account's context.
//
// Format: rt1.<run uuid>.<expiry unix seconds>.<hex HMAC-SHA256(key, "rt1.<run>.<expiry>")>.
// The alphabet is the worker's bearer-token charset. Tokens are stateless (no table, no
// migration); whether the run still accepts pulls is checked against agent_runs.status at pull time.
package runtoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	prefix = "rt1"
	// MinKeyBytes is the shortest signing key accepted.
	MinKeyBytes = 32
	// DefaultTTL is how long a freshly minted token stays valid.
	DefaultTTL = 15 * time.Minute
	// MaxTTL caps what Issue will mint.
	MaxTTL = time.Hour
	// maxTokenLen bounds what Verify will even parse.
	maxTokenLen = 512
)

// ErrInvalid is returned for every token that cannot be accepted: malformed, wrongly signed or
// expired. Callers must not tell the client which.
var ErrInvalid = errors.New("runtoken: invalid token")

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// DeriveKey derives a signing key from another secret (the operator API token), domain-separated
// so the derived key cannot be used as, or recovered into, that secret.
func DeriveKey(secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("ghost/run-token/v1"))
	return mac.Sum(nil)
}

// Signer mints and verifies tokens with one key.
type Signer struct {
	key []byte
	ttl time.Duration
}

// NewSigner returns a Signer. The key must be at least MinKeyBytes long; ttl must be in (0, MaxTTL].
func NewSigner(key []byte, ttl time.Duration) (*Signer, error) {
	if len(key) < MinKeyBytes {
		return nil, fmt.Errorf("runtoken: signing key must be at least %d bytes", MinKeyBytes)
	}
	if ttl <= 0 || ttl > MaxTTL {
		return nil, fmt.Errorf("runtoken: ttl must be in (0, %s]", MaxTTL)
	}
	return &Signer{key: append([]byte(nil), key...), ttl: ttl}, nil
}

// TTL is how long a token this signer mints stays valid.
func (s *Signer) TTL() time.Duration { return s.ttl }

// Issue mints a token for runID valid from now for the signer's ttl.
func (s *Signer) Issue(runID string, now time.Time) (string, error) {
	if !uuidPattern.MatchString(runID) {
		return "", errors.New("runtoken: run id must be a lowercase uuid")
	}
	body := fmt.Sprintf("%s.%s.%d", prefix, runID, now.Add(s.ttl).Unix())
	return body + "." + s.sign(body), nil
}

// Verify checks the signature (in constant time) and the expiry and returns the run id.
func (s *Signer) Verify(token string, now time.Time) (string, error) {
	if len(token) == 0 || len(token) > maxTokenLen {
		return "", ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != prefix || !uuidPattern.MatchString(parts[1]) {
		return "", ErrInvalid
	}
	body := strings.Join(parts[:3], ".")
	got, err := hex.DecodeString(parts[3])
	if err != nil || parts[3] != strings.ToLower(parts[3]) || !hmac.Equal(got, s.mac(body)) {
		return "", ErrInvalid
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	// A token never outlives MaxTTL, whatever expiry a (buggy or compromised) minter signed.
	if err != nil || !now.Before(time.Unix(exp, 0)) || time.Unix(exp, 0).Sub(now) > MaxTTL {
		return "", ErrInvalid
	}
	return parts[1], nil
}

func (s *Signer) sign(body string) string { return hex.EncodeToString(s.mac(body)) }

func (s *Signer) mac(body string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(body))
	return m.Sum(nil)
}
