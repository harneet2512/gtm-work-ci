package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/slack-go/slack"

	"github.com/harneet2512/gtm-work/core-go/internal/codespace"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// codespaceDrive is the live walk of one played case in the real Slack channel (the bot must be running): Play, then the
// scripted human acting through the same Slack handlers. args are the arguments after the flags: exactly one case name.
func codespaceDrive(ctx context.Context, rt codespace.Runtime, args []string, out io.Writer) error {
	if len(args) != 1 || rt.Cfg.NoSlack {
		return errors.New("ghostctl: codespace drive <case1|case2> needs the stack up with Slack on")
	}
	poster := slacksurface.NewSlackPoster(slack.New(rt.Cfg.Slack["SLACK_BOT_TOKEN"]))
	st, path, err := rt.Drive(ctx, args[0], poster)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"case": path.Case, "run_id": st.RunID, "episode_id": st.EpisodeID, "manifest_id": st.ManifestID})
}
