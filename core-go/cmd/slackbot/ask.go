package main

import (
	"context"
	"log/slog"

	"github.com/slack-go/slack"

	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// newAskHandler wires Ask Cliff: core's POST /ask client, the Slack poster and Cliff's own identity (auth.test), so the
// bot ignores its own messages and strips its own mention from a question.
func newAskHandler(ctx context.Context, cfg slacksurface.Config, api *slack.Client, log *slog.Logger) (*slacksurface.AskHandler, error) { // error kept for later wiring
	var user, bot string
	if id, err := api.AuthTestContext(ctx); err != nil {
		// Not fatal: every message of a bot, Cliff's own included, carries a bot id and is ignored; only the removal of
		// the mention from the question text is lost.
		log.Warn("ask: could not read Cliff's own identity (auth.test)", "error", err)
	} else {
		user, bot = id.UserID, id.BotID
	}
	core := slacksurface.NewAskHTTP(cfg.CoreURL, cfg.APIToken, nil)
	if len(cfg.AskAllowedUsers) == 0 {
		log.Warn("ask: " + slacksurface.EnvAskAllowedUsers + " is not set, so anyone who can DM Cliff or mention it in a channel it is in can ask it questions (read only)")
	}
	return slacksurface.NewAskHandler(core, slacksurface.NewSlackAskPoster(api), user, bot, log,
		slacksurface.WithAskAllowedUsers(cfg.AskAllowedUsers)), nil
}
