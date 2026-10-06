package controlplane

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// decodeIDs reads a jsonb array of uuid strings (to_jsonb of a uuid[]); null reads as none.
func decodeIDs(raw []byte) ([]string, error) {
	ids := []string{}
	if len(raw) == 0 {
		return ids, nil
	}
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, fmt.Errorf("controlplane: decode id list: %w", err)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

// stableID is a version 5 uuid of the name parts under the namespace uuid: the same inputs always give the same id, so a
// derived row (a knowledge mutation, a trace span) keeps its id across reads without being stored.
func stableID(namespace string, parts ...string) string {
	ns, _ := hex.DecodeString(strings.ReplaceAll(namespace, "-", ""))
	h := sha1.New()
	h.Write(ns)
	h.Write([]byte(strings.Join(parts, "\x00")))
	sum := h.Sum(nil)[:16]
	sum[6] = sum[6]&0x0f | 0x50
	sum[8] = sum[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
