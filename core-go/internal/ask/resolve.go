package ask

import (
	"context"
	"strings"
)

const maxAccountPages = 5

type account struct{ ID, Name string }

// listAll reads the account list (the same page the web home reads), at most maxAccountPages pages.
func (t *Tools) listAll(ctx context.Context) ([]any, error) {
	var items []any
	cursor := ""
	for page := 0; page < maxAccountPages; page++ {
		v, _, err := t.get(ctx, "/accounts"+q(map[string]string{"limit": "200", "cursor": cursor}))
		if err != nil {
			return nil, err
		}
		m := obj(v)
		items = append(items, arr(m["items"])...)
		if cursor = text(m["next_cursor"]); cursor == "" {
			break
		}
	}
	return items, nil
}

// resolveAccount accepts an account id or a name (exact, then a single partial match, ignoring case).
// A message is returned instead of an account when the reference names none or several.
func (t *Tools) resolveAccount(ctx context.Context, ref string) (acc account, msg string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return account{}, "name an account", nil
	}
	if uuidRe.MatchString(ref) {
		v, status, err := t.get(ctx, "/accounts/"+ref)
		if err != nil {
			return account{}, "", err
		}
		if status == 404 {
			return account{}, "no account has that id", nil
		}
		return account{ID: ref, Name: text(obj(v)["name"])}, "", nil
	}
	items, err := t.listAll(ctx)
	if err != nil {
		return account{}, "", err
	}
	want := strings.ToLower(ref)
	var partial []account
	for _, it := range items {
		m := obj(it)
		a := account{ID: text(m["id"]), Name: text(m["name"])}
		switch name := strings.ToLower(a.Name); {
		case name == want:
			return a, "", nil
		case strings.Contains(name, want) || strings.Contains(want, name) && name != "":
			partial = append(partial, a)
		}
	}
	switch len(partial) {
	case 1:
		return partial[0], "", nil
	case 0:
		return account{}, "no account is named like " + ref, nil
	}
	names := make([]string, len(partial))
	for i, p := range partial {
		names[i] = p.Name
	}
	return account{}, "several accounts match: " + strings.Join(names, ", "), nil
}

// latestRun is the account's newest agent run and its decision episode (GET /runs, newest first).
func (t *Tools) latestRun(ctx context.Context, accountID string) (runID, episodeID string, err error) {
	v, _, err := t.get(ctx, "/runs"+q(map[string]string{"account_id": accountID, "limit": "1"}))
	if err != nil {
		return "", "", err
	}
	items := arr(obj(v)["items"])
	if len(items) == 0 {
		return "", "", nil
	}
	run := obj(items[0])
	return text(run["id"]), text(obj(run["generation"])["decision_episode_id"]), nil
}

// target is what a run or episode tool is about.
type target struct {
	RunID, EpisodeID string
	Account          account
}

// resolveTarget finds the run and episode a tool names: a run id, an episode id ("latest" or none means the account's
// newest) or an account. msg explains why none was found.
func (t *Tools) resolveTarget(ctx context.Context, args map[string]any) (tg target, msg string, err error) {
	run, ep, ref := str(args, "run_id"), str(args, "episode_id"), str(args, "account")
	if ep == "latest" {
		ep = ""
	}
	for _, id := range []string{run, ep} {
		if id != "" && !uuidRe.MatchString(id) {
			return tg, "ids are uuids; name the account instead", nil
		}
	}
	if ref != "" {
		if tg.Account, msg, err = t.resolveAccount(ctx, ref); err != nil || msg != "" {
			return tg, msg, err
		}
	}
	tg.RunID, tg.EpisodeID = run, ep
	switch {
	case ep != "" && run == "":
		v, status, err := t.get(ctx, "/episodes/"+ep)
		if err != nil {
			return tg, "", err
		}
		if status == 404 {
			return tg, "no episode has that id", nil
		}
		tg.RunID = text(obj(v)["agent_run_id"])
		tg.Account = account{ID: text(obj(v)["account_id"]), Name: text(obj(v)["account_name"])}
	case run != "" && ep == "":
		v, status, err := t.get(ctx, "/runs/"+run)
		if err != nil {
			return tg, "", err
		}
		if status == 404 {
			return tg, "no run has that id", nil
		}
		tg.EpisodeID = text(obj(obj(v)["generation"])["decision_episode_id"])
	case run == "" && ep == "":
		if tg.Account.ID == "" {
			return tg, "name an account, a run or an episode", nil
		}
		if tg.RunID, tg.EpisodeID, err = t.latestRun(ctx, tg.Account.ID); err != nil {
			return tg, "", err
		}
		if tg.RunID == "" {
			return tg, "this account has no run yet", nil
		}
	}
	return tg, "", nil
}
