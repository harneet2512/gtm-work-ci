package corectx

// WithheldVisibility is the `withheld` marker of a value the agent may not see.
const WithheldVisibility = "visibility"

// withhold removes what the agent may not see from one unclipped item, recursively. It runs before
// clipping so a long evidence list cannot push a hidden reference out of sight.
//
// A map whose evidence_refs include a hidden activity is withheld as a whole:
//   - a state field ({field_path, known, value, ...}) becomes
//     {field_path, known: false, value: null, evidence_refs: [], withheld: "visibility"};
//   - a diff change ({field, op, ...}) loses before, after and its refs and gains withheld;
//   - any other item (a buying-group member, a commitment or list element) becomes
//     {known: false, value: null, withheld: "visibility"}.
//
// Hidden activity ids are also dropped from every activity_ids list. flagged is set whenever
// anything was withheld or dropped.
func withhold(v any, key string, hidden map[string]bool, flagged *bool) any {
	if len(hidden) == 0 {
		return v
	}
	switch x := v.(type) {
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			if id, ok := e.(string); ok && key == "activity_ids" && hidden[id] {
				*flagged = true
				continue
			}
			out = append(out, withhold(e, key, hidden, flagged))
		}
		return out
	case map[string]any:
		if hasHiddenEvidence(x, hidden) {
			*flagged = true
			return withheldItem(x)
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = withhold(e, k, hidden, flagged)
		}
		return out
	default:
		return v
	}
}

func hasHiddenEvidence(m map[string]any, hidden map[string]bool) bool {
	refs, _ := m["evidence_refs"].([]any)
	for _, r := range refs {
		ref, _ := r.(map[string]any)
		if id, _ := ref["activity_id"].(string); hidden[id] {
			return true
		}
	}
	return false
}

func withheldItem(m map[string]any) map[string]any {
	if _, isChange := m["op"]; isChange && m["field"] != nil {
		return map[string]any{"field": m["field"], "op": m["op"], "material": m["material"], "withheld": WithheldVisibility}
	}
	out := map[string]any{"known": false, "value": nil, "withheld": WithheldVisibility}
	if fp, ok := m["field_path"]; ok {
		out["field_path"] = fp
		out["evidence_refs"] = []any{}
	}
	return out
}
