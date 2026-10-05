// Package dedupe derives the idempotency key of a source event (contracts/normalization.md).
package dedupe

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"unicode/utf8"
)

// IdempotencyKey returns hex(sha256(len(system) ":" system "|" len(object) ":" object "|"
// len(event) ":" event)). Lengths are counted in characters (runes), and every part is
// length-prefixed so that no two distinct triples serialize to the same string: ("a|b","c","d")
// and ("a","b|c","d") yield different keys. The authoritative duplicate guard is the database
// UNIQUE (source_system, source_object_id, source_event_key); the key is a derived lookup value.
func IdempotencyKey(system, object, event string) string {
	h := sha256.New()
	for i, part := range [3]string{system, object, event} {
		if i > 0 {
			h.Write([]byte{'|'})
		}
		h.Write([]byte(strconv.Itoa(utf8.RuneCountInString(part))))
		h.Write([]byte{':'})
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
