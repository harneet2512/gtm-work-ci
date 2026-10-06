package corectx

import (
	"encoding/json"
	"sort"
	"unicode/utf8"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// Per-item clipping, so one hostile or huge field cannot crowd out the rest of a packet.
const (
	maxStringRunes  = 500
	maxArrayItems   = 20
	maxEvidenceRefs = 5
	maxDepth        = 8
)

// idKeys are the keys whose uuid values are reported as the pull's returned_ids.
var idKeys = map[string]bool{"activity_id": true, "claim_id": true, "person_id": true, "id": true}

type bounded struct {
	items     []json.RawMessage
	ids       []string
	bytes     int
	truncated bool
}

// bound clips every item, keeps at most limit items whose serialized total stays within
// MaxPacketBytes (an item that cannot fit alone is dropped), and collects the returned ids.
// truncated is true whenever anything was clipped or left out.
func bound(items []any, limit int, hidden map[string]bool) bounded {
	out := bounded{items: []json.RawMessage{}}
	seen := map[string]bool{}
	size := 2 // the enclosing []
	for i, item := range items {
		if i >= limit {
			out.truncated = true
			break
		}
		generic, ok := toGeneric(item)
		if !ok {
			out.truncated = true
			continue
		}
		// Withhold before clipping: a clipped evidence list could hide the hidden reference.
		clipped := false
		generic = withhold(generic, "", hidden, &clipped)
		generic = clip(generic, "", 0, &clipped)
		raw, err := json.Marshal(generic)
		switch {
		case err != nil || len(raw)+2 > MaxPacketBytes:
			out.truncated = true // this item cannot fit even alone: drop it, keep the rest
			continue
		case size+len(raw)+1 > MaxPacketBytes:
			out.truncated = true
			return finish(out) // the packet is full; later items stay out so the order is intact
		}
		size += len(raw) + 1
		out.truncated = out.truncated || clipped
		out.items = append(out.items, raw)
		collectIDs(generic, seen, &out.ids)
	}
	return finish(out)
}

// finish sets bytes to the exact serialized size of the kept items.
func finish(b bounded) bounded {
	raw, err := json.Marshal(b.items)
	if err == nil {
		b.bytes = len(raw)
	}
	return b
}

func toGeneric(v any) (any, bool) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, false
	}
	return generic, true
}

// clip returns v with long strings, long arrays, evidence lists and deep nesting cut down.
func clip(v any, key string, depth int, clipped *bool) any {
	if depth > maxDepth {
		*clipped = true
		return nil
	}
	switch x := v.(type) {
	case string:
		if utf8.RuneCountInString(x) > maxStringRunes {
			*clipped = true
			return string([]rune(x)[:maxStringRunes]) + "..."
		}
		return x
	case []any:
		limit := maxArrayItems
		if key == "evidence_refs" {
			limit = maxEvidenceRefs
		}
		if len(x) > limit {
			*clipped = true
			x = x[:limit]
		}
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = clip(e, key, depth+1, clipped)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = clip(e, k, depth+1, clipped)
		}
		return out
	default:
		return v
	}
}

// collectIDs appends the uuids found under idKeys, in a deterministic order, once each.
func collectIDs(v any, seen map[string]bool, ids *[]string) {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			collectIDs(e, seen, ids)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s, ok := x[k].(string); ok && idKeys[k] && readmodel.ValidUUID(s) {
				if !seen[s] {
					seen[s] = true
					*ids = append(*ids, s)
				}
				continue
			}
			collectIDs(x[k], seen, ids)
		}
	}
}
