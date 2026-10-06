// Package payloadhash is the digest a demo manifest pins for the payload of its held-out event: the lowercase
// hex SHA-256 of the payload's canonical JSON. Freezing (demomine) and Play (play) both use it, so a replay
// dataset that was edited after the case was frozen is refused before anything is released.
package payloadhash

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// SHA256 returns the digest of payload. Canonical JSON: object keys sorted at every level, no insignificant
// whitespace, numbers as written, strings with no HTML escaping, no trailing newline (the same form as the
// manifest's content_sha256).
func SHA256(payload []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("payloadhash: the payload is not JSON: %w", err)
	}
	if dec.More() {
		return "", fmt.Errorf("payloadhash: the payload has data after its JSON value")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil { // map keys are written sorted
		return "", fmt.Errorf("payloadhash: canonical encode: %w", err)
	}
	sum := sha256.Sum256(bytes.TrimRight(buf.Bytes(), "\n"))
	return hex.EncodeToString(sum[:]), nil
}
