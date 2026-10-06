package ask

import (
	"context"
	"time"
)

// DraftLabel and PreviewLabel are put in front of every answer that rests on a dry-run tool.
const (
	DraftLabel   = "DRAFT (dry run, nothing was sent)"
	PreviewLabel = "PREVIEW (nothing was written to the CRM)"
)

// draftFollowup gathers what a follow-up should rest on through the existing strategy path: the account's newest
// strategy set (its preferred candidate) and the people. It never creates a run and never sends.
func (t *Tools) draftFollowup(ctx context.Context, args map[string]any) (ToolResult, error) {
	intent := str(args, "intent")
	if intent == "" {
		return ToolResult{}, badArgs("intent is required")
	}
	acc, miss, err := t.accountOrMessage(ctx, args)
	if err != nil || miss != nil {
		return orMiss(miss), err
	}
	data := map[string]any{"label": DraftLabel, "intent": intent, "account": acc.Name,
		"rule": "Write the draft yourself from this, label it DRAFT and say nothing was sent."}
	runID, episodeID, err := t.latestRun(ctx, acc.ID)
	if err != nil {
		return ToolResult{}, err
	}
	if runID != "" {
		if v, status, err := t.get(ctx, "/runs/"+runID+"/strategies"); err != nil {
			return ToolResult{}, err
		} else if status == 200 {
			data["preferred_strategy"] = t.preferred(ctx, acc.ID, v)
		}
	}
	if st, _, err := t.get(ctx, "/accounts/"+acc.ID+"/state"); err == nil && st != nil {
		data["state"] = st
	}
	res := found(data, Link{Label: acc.Name + " in gtm_ai", URL: t.L.Account(acc.ID)}, Link{Label: "Episode", URL: t.L.Episode(episodeID)})
	res.DryRun = true
	return res, nil
}

// preferred picks the agent's preferred candidate of a strategy set and names its recipients.
func (t *Tools) preferred(ctx context.Context, accountID string, set any) any {
	cands := arr(obj(obj(set)["strategy_set"])["candidates"])
	if len(cands) == 0 {
		cands = arr(obj(set)["candidates"])
	}
	people := t.people(ctx, accountID)
	for _, c := range cands {
		m := obj(c)
		if p, _ := m["preferred_by_agent"].(bool); p || len(cands) == 1 {
			return map[string]any{"strategy": m["strategy_type"], "to": names(arr(m["to"]), people), "cc": names(arr(m["cc"]), people),
				"subject": m["subject"], "artifact": m["full_action_artifact"], "rationale": m["rationale"]}
		}
	}
	return map[string]any{"note": "no candidate is marked as preferred"}
}

func (t *Tools) people(ctx context.Context, accountID string) map[string]string {
	out := map[string]string{}
	cutoff := t.now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	g, _, err := t.get(ctx, "/accounts/"+accountID+"/graph"+q(map[string]string{"world_as_of": cutoff, "limit": "20"}))
	if err != nil {
		return out
	}
	for _, n := range arr(obj(g)["nodes"]) {
		if m := obj(n); text(m["type"]) == "person" {
			out[text(m["id"])] = text(m["label"])
		}
	}
	return out
}

func names(recipients []any, people map[string]string) []string {
	out := make([]string, 0, len(recipients))
	for _, r := range recipients {
		id := text(obj(r)["person_id"])
		if n := people[id]; n != "" {
			out = append(out, n)
		} else {
			out = append(out, "a person on the account")
		}
	}
	return out
}

// crmUpdatePreview shows what a CRM field change would write. It only reads the state; it writes nothing.
func (t *Tools) crmUpdatePreview(ctx context.Context, args map[string]any) (ToolResult, error) {
	field, value := str(args, "field"), str(args, "value")
	if field == "" || value == "" {
		return ToolResult{}, badArgs("field and value are required")
	}
	acc, miss, err := t.accountOrMessage(ctx, args)
	if err != nil || miss != nil {
		return orMiss(miss), err
	}
	st, _, err := t.get(ctx, "/accounts/"+acc.ID+"/state")
	if err != nil {
		return ToolResult{}, err
	}
	current, ok := findKey(st, field)
	data := map[string]any{"label": PreviewLabel, "account": acc.Name, "would_write": map[string]any{"field": field, "from": current, "to": value},
		"note": "Nothing is written. This is what would change."}
	if !ok {
		data["note"] = "That field is not in the account state, so there is no current value. Nothing is written."
	}
	res := found(data, Link{Label: acc.Name + " in gtm_ai", URL: t.L.Account(acc.ID)})
	res.DryRun = true
	return res, nil
}

// findKey finds the first member named key anywhere in v, depth first.
func findKey(v any, key string) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		if val, ok := x[key]; ok {
			return val, true
		}
		for _, e := range x {
			if val, ok := findKey(e, key); ok {
				return val, true
			}
		}
	case []any:
		for _, e := range x {
			if val, ok := findKey(e, key); ok {
				return val, true
			}
		}
	}
	return nil, false
}
