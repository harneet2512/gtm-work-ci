package play

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// volatileKeys are the members a state digest must not compare: per-run stamps and bookkeeping times whose
// values legitimately differ between two identical replays. World-time stamps (occurred_at, as_of, valid_from,
// closed_at, decided_at...) stay: they are the semantic content two replays must agree on.
var volatileKeys = map[string]bool{
	"computed_at": true, "created_at": true, "updated_at": true, "generated_at": true,
	"evaluated_at": true, "received_at": true, "projected_at": true, "claimed_at": true,
	"completed_at": true, "enqueued_at": true, "lease_expires_at": true, "adjudicated_at": true,
	"last_delivered_at": true, "last_updated_at": true, "released_at": true, "reset_at": true,
	"advanced_at": true, "send_decided_at": true, "verdict_at": true, "confirmed_at": true,
}

// digestOf is the lowercase hex SHA-256 of a JSON document's semantic content (episode_replay.v1.json
// episodeState.digest / stateSnapshot.digest): the document canonicalized — volatile members dropped, uuid
// values masked — and marshalled with sorted keys.
func digestOf(raw json.RawMessage) (string, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("play: decode the document to digest: %w", err)
	}
	canonical, err := json.Marshal(canonicalize(doc))
	if err != nil {
		return "", fmt.Errorf("play: canonicalize the document to digest: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalize drops volatile members, masks uuid string values with "<uuid>" (an id's position is structure,
// its value is a per-run detail) and recurses.
func canonicalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			if volatileKeys[k] {
				continue
			}
			out[k] = canonicalize(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = canonicalize(e)
		}
		return out
	case string:
		if IsUUID(t) {
			return "<uuid>"
		}
		return t
	default:
		return t
	}
}
