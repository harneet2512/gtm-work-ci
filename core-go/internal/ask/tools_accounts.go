package ask

import (
	"context"
	"strings"
	"time"
)

func (t *Tools) listAccounts(ctx context.Context, _ map[string]any) (ToolResult, error) {
	items, err := t.listAll(ctx)
	if err != nil {
		return ToolResult{}, err
	}
	rows := make([]any, 0, len(items))
	for _, it := range items {
		m := obj(it)
		rows = append(rows, map[string]any{"name": m["name"], "stage": m["stage"], "health": m["health"],
			"motion": m["motion"], "last_meaningful_change": m["last_meaningful_change"], "last_activity_at": m["last_activity_at"]})
	}
	return found(rows, Link{Label: "gtm_ai accounts", URL: t.L.Base()}), nil
}

func (t *Tools) accountOrMessage(ctx context.Context, args map[string]any) (account, *ToolResult, error) {
	acc, msg, err := t.resolveAccount(ctx, str(args, "account"))
	if err != nil {
		return acc, nil, err
	}
	if msg != "" {
		r := notFound(msg)
		return acc, &r, nil
	}
	return acc, nil, nil
}

// worldCutoff is the world_as_of of an as_of argument: a date includes that day. Without one, it is the present.
func (t *Tools) worldCutoff(args map[string]any, key string) (cutoff time.Time, given bool, err error) {
	raw := str(args, key)
	if raw == "" {
		return t.now().UTC().Add(24 * time.Hour), false, nil
	}
	at, dateOnly, err := parseWhen(raw)
	if err != nil {
		return cutoff, false, err
	}
	return endOf(at, dateOnly), true, nil
}

func (t *Tools) accountState(ctx context.Context, args map[string]any) (ToolResult, error) {
	acc, miss, err := t.accountOrMessage(ctx, args)
	if err != nil || miss != nil {
		return orMiss(miss), err
	}
	cutoff, given, err := t.worldCutoff(args, "as_of")
	if err != nil {
		return ToolResult{}, err
	}
	path := "/accounts/" + acc.ID + "/state"
	if given {
		path += q(map[string]string{"world_as_of": cutoff.Format(time.RFC3339)})
	}
	state, code, err := t.getCode(ctx, path)
	if err != nil {
		return ToolResult{}, err
	}
	data := map[string]any{"account": acc.Name, "state": state}
	if state == nil {
		data["state"] = "no state was computed " + map[bool]string{true: "before that time", false: "yet"}[given] + " (" + code + ")"
	}
	if bi, _, err := t.get(ctx, "/accounts/"+acc.ID+"/business-intelligence/latest"); err == nil && bi != nil {
		data["latest_business_update"] = bi
	}
	if !given {
		if d, _, err := t.get(ctx, "/accounts/"+acc.ID+"/diffs?limit=5"); err == nil && d != nil {
			data["recent_material_changes"] = obj(d)["items"]
		}
	}
	res := found(data, Link{Label: acc.Name + " in gtm_ai", URL: t.L.Account(acc.ID)})
	res.Empty = state == nil && data["latest_business_update"] == nil
	return res, nil
}

func orMiss(miss *ToolResult) ToolResult {
	if miss != nil {
		return *miss
	}
	return ToolResult{}
}

func (t *Tools) timeline(ctx context.Context, args map[string]any) (ToolResult, error) {
	acc, miss, err := t.accountOrMessage(ctx, args)
	if err != nil || miss != nil {
		return orMiss(miss), err
	}
	items, err := t.activities(ctx, acc.ID, str(args, "from"), str(args, "to"), 200)
	if err != nil {
		return ToolResult{}, err
	}
	n := intArg(args, "limit", 10, 50)
	if len(items) > n {
		items = items[:n]
	}
	return found(items, Link{Label: acc.Name + " timeline", URL: t.L.Account(acc.ID)}), nil
}

// activities reads the account timeline (newest first), keeping those from..to (a date names its whole day).
func (t *Tools) activities(ctx context.Context, accountID, from, to string, limit int) ([]any, error) {
	params := map[string]string{"limit": "200"}
	var lo time.Time
	if to != "" {
		at, dateOnly, err := parseWhen(to)
		if err != nil {
			return nil, err
		}
		params["before"] = endOf(at, dateOnly).Format(time.RFC3339)
	}
	if from != "" {
		at, _, err := parseWhen(from)
		if err != nil {
			return nil, err
		}
		lo = at
	}
	v, status, err := t.get(ctx, "/accounts/"+accountID+"/timeline"+q(params))
	if err != nil || status == 404 {
		return nil, err
	}
	var out []any
	for _, it := range arr(obj(v)["items"]) {
		if !lo.IsZero() {
			if at, err := time.Parse(time.RFC3339, text(obj(it)["occurred_at"])); err == nil && at.Before(lo) {
				continue
			}
		}
		out = append(out, it)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (t *Tools) graphNeighborhood(ctx context.Context, args map[string]any) (ToolResult, error) {
	acc, miss, err := t.accountOrMessage(ctx, args)
	if err != nil || miss != nil {
		return orMiss(miss), err
	}
	cutoff, _, err := t.worldCutoff(args, "as_of")
	if err != nil {
		return ToolResult{}, err
	}
	v, status, err := t.get(ctx, "/accounts/"+acc.ID+"/graph"+q(map[string]string{"world_as_of": cutoff.Format(time.RFC3339), "limit": "5"}))
	if err != nil {
		return ToolResult{}, err
	}
	if status == 404 {
		return notFound("no graph for that account"), nil
	}
	return found(v, Link{Label: acc.Name + " account graph", URL: t.L.Account(acc.ID)}), nil
}

func (t *Tools) searchActivities(ctx context.Context, args map[string]any) (ToolResult, error) {
	query := strings.ToLower(str(args, "query"))
	if query == "" {
		return ToolResult{}, badArgs("query is required")
	}
	var accs []account
	if str(args, "account") != "" {
		acc, miss, err := t.accountOrMessage(ctx, args)
		if err != nil || miss != nil {
			return orMiss(miss), err
		}
		accs = []account{acc}
	} else {
		items, err := t.listAll(ctx)
		if err != nil {
			return ToolResult{}, err
		}
		for _, it := range items[:min(len(items), 10)] {
			accs = append(accs, account{ID: text(obj(it)["id"]), Name: text(obj(it)["name"])})
		}
	}
	var hits []any
	var links []Link
	for _, acc := range accs {
		acts, err := t.activities(ctx, acc.ID, "", "", 200)
		if err != nil {
			return ToolResult{}, err
		}
		n := 0
		for _, a := range acts {
			if strings.Contains(strings.ToLower(jsonText(a)), query) && n < 5 {
				hits = append(hits, map[string]any{"account": acc.Name, "activity": a})
				n++
			}
		}
		if n > 0 {
			links = append(links, Link{Label: acc.Name + " timeline", URL: t.L.Account(acc.ID)})
		}
	}
	return found(hits, links...), nil
}
