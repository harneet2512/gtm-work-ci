package ask

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Ask tokens are `at1.<expiry unix s>.<nonce hex>.<hex HMAC-SHA256(key, "at1.<expiry>.<nonce>")>`: stateless, short
// lived, and signed with a key derived under a different label than the run token's, so neither is accepted as the
// other. They authorise the read tools of one question and nothing else.
const (
	tokenPrefix = "at1"
	// TokenTTL is how long a question may keep calling tools (the worker's deadline is 60 s).
	TokenTTL      = 2 * time.Minute
	maxTokenChars = 256
)

// Signer mints and verifies ask tokens.
type Signer struct{ key []byte }

// NewSigner derives the ask key from secret (the operator token or GHOST_RUN_TOKEN_SECRET), domain separated.
func NewSigner(secret []byte) (*Signer, error) {
	if len(secret) < 32 {
		return nil, errors.New("ask: signing secret must be at least 32 bytes")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("ghost/ask-token/v1"))
	return &Signer{key: mac.Sum(nil)}, nil
}

func (s *Signer) mac(body string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

// Issue mints a token valid for TokenTTL from now.
func (s *Signer) Issue(now time.Time) (string, error) {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("ask: nonce: %w", err)
	}
	body := fmt.Sprintf("%s.%d.%s", tokenPrefix, now.Add(TokenTTL).Unix(), hex.EncodeToString(nonce))
	return body + "." + s.mac(body), nil
}

// Verify checks the signature in constant time and the expiry.
func (s *Signer) Verify(token string, now time.Time) error {
	if token == "" || len(token) > maxTokenChars {
		return ErrBadToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != tokenPrefix {
		return ErrBadToken
	}
	want := s.mac(strings.Join(parts[:3], "."))
	if !hmac.Equal([]byte(want), []byte(parts[3])) {
		return ErrBadToken
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || !now.Before(time.Unix(exp, 0)) || time.Unix(exp, 0).Sub(now) > TokenTTL {
		return ErrBadToken
	}
	return nil
}
