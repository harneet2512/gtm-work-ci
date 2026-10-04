package demomine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// manifestID is a function of the Salesforce opportunity and the held-out event only, never of a
// database-assigned id, so two freezes of the same case carry the same id.
func manifestID(o FreezeOptions, heldOutEventID string) string {
	return uuidV5(eventNamespace, "manifest\x00"+o.OpportunityID+"\x00"+heldOutEventID)
}

// seal sets content_sha256 (the digest of the canonical projection of the document, see canonical) and returns
// the manifest as indented JSON.
func seal(m *Manifest) ([]byte, error) {
	m.ContentSHA256 = ""
	canon, err := canonical(m, "content_sha256")
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canon)
	m.ContentSHA256 = hex.EncodeToString(sum[:])
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("demomine: encode manifest: %w", err)
	}
	return append(raw, '\n'), nil
}

// VerifyHash recomputes content_sha256 of a manifest document (JSON) and reports whether it matches.
func VerifyHash(doc []byte) error {
	var generic map[string]any
	if err := json.Unmarshal(doc, &generic); err != nil {
		return fmt.Errorf("demomine: decode manifest: %w", err)
	}
	want, _ := generic["content_sha256"].(string)
	canon, err := canonical(generic, "content_sha256")
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canon)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("demomine: content_sha256 is %s, the document hashes to %s", want, got)
	}
	return nil
}

// canonical is the hashed form of a manifest: the document without content_sha256 and without the
// database-assigned ids (account_id, opportunity_id, events[].state_after.account_id / .opportunity_id,
// events[].state_diff_id), so the hash is a function of the replayed content and created_at alone and the
// same case hashes the same in any database. Encoding: object keys sorted, no insignificant whitespace, no
// HTML escaping (so "<" and "&" stay literal).
func canonical(v any, drop string) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("demomine: encode for hashing: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic map[string]any
	if err := dec.Decode(&generic); err != nil {
		return nil, fmt.Errorf("demomine: decode for hashing: %w", err)
	}
	delete(generic, drop)
	dropDatabaseIDs(generic)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(generic); err != nil { // map keys are written sorted
		return nil, fmt.Errorf("demomine: canonical encode: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// dropDatabaseIDs removes the ids the database assigned while the world was materialized.
func dropDatabaseIDs(doc map[string]any) {
	delete(doc, "account_id")
	delete(doc, "opportunity_id")
	events, _ := doc["events"].([]any)
	for _, e := range events {
		ev, ok := e.(map[string]any)
		if !ok {
			continue
		}
		delete(ev, "state_diff_id")
		if st, ok := ev["state_after"].(map[string]any); ok {
			delete(st, "account_id")
			delete(st, "opportunity_id")
		}
	}
}
