// Command slackbot is the Slack Socket Mode surface for the Ghost demo (HAR-112 / HAR-123).
//
// It is a separate binary from core on purpose: it only speaks HTTP to core, so core carries no
// Slack dependency and a Slack outage cannot affect ingestion or state. Live mode reads
// SLACK_APP_TOKEN, SLACK_BOT_TOKEN and SLACK_CHANNEL_ID (plus CORE_URL and GHOST_API_TOKEN) from the
// environment only; --dry-run needs none of them.
//
//	slackbot --dry-run [--out previews.json]       print Block Kit JSON for the fixture episode
//	slackbot                                        listen for interactions (Socket Mode); also posts Message 2 for every
//	                                                strategy set core publishes (SLACK_OUTBOX=off disables)
//	slackbot --post-run RUN --post-account ACCT     post M1 (account) and M2 (run); --message bi|chooser|judgment|play
//	         [--post-episode EP] [--message judgment]
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("slackbot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dry := fs.Bool("dry-run", false, "print the Block Kit JSON for a fixture episode and exit (no Slack, no core)")
	out := fs.String("out", "", "with --dry-run: write JSON to this file instead of stdout")
	pa := postArgs{}
	fs.StringVar(&pa.run, "post-run", "", "post messages for this run id and exit (chooser, judgment)")
	fs.StringVar(&pa.account, "post-account", "", "account id whose latest business-intelligence update Message 1 shows")
	fs.StringVar(&pa.episode, "post-episode", "", "decision episode id (judgment message)")
	fs.StringVar(&pa.message, "message", "play", "with --post-run: play (bi then chooser), bi, chooser or judgment")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dry {
		return dryRun(*out, stdout, stderr)
	}
	cfg, err := slacksurface.LoadConfig(getenv)
	if err != nil {
		fmt.Fprintln(stderr, err) // names variables only, never values
		return 2
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runLive(ctx, cfg, log, pa, ""); err != nil {
		log.Error("slackbot stopped", "error", err)
		return 1
	}
	return 0
}

func dryRun(outPath string, stdout, stderr io.Writer) int {
	ps, err := slacksurface.Previews(slacksurface.NewFixture())
	if err != nil {
		fmt.Fprintln(stderr, "dry-run:", err)
		return 1
	}
	w := stdout
	if outPath != "" {
		f, err := os.Create(outPath)
		if err != nil {
			fmt.Fprintln(stderr, "dry-run:", err)
			return 1
		}
		w = f
		defer func() {
			if err := f.Close(); err != nil {
				fmt.Fprintln(stderr, "dry-run:", err)
			}
		}()
	}
	if err := slacksurface.WritePreviews(w, ps); err != nil {
		fmt.Fprintln(stderr, "dry-run:", err)
		return 1
	}
	return 0
}

func runLive(ctx context.Context, cfg slacksurface.Config, log *slog.Logger, pa postArgs, apiURL string) error {
	core := slacksurface.NewCoreHTTP(cfg.CoreURL, cfg.APIToken, nil)
	api, smc := slacksurface.NewSlackClients(cfg, apiURL)
	var poster slacksurface.Poster = slacksurface.NewSlackPoster(api)
	if cfg.AuditFile != "" {
		f, err := os.OpenFile(cfg.AuditFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open the audit file: %w", err)
		}
		defer f.Close()
		poster = slacksurface.NewAuditPoster(poster, f)
	}
	h := slacksurface.NewHandler(core, poster, cfg.ChannelID, cfg.WebURL, log, slacksurface.WithAllowedUsers(cfg.AllowedUsers))
	if pa.run != "" || pa.account != "" {
		return post(ctx, h.Publisher(), pa)
	}
	if err := cfg.RequireAuthorization(); err != nil {
		return err
	}
	if len(cfg.AllowedUsers) == 0 {
		log.Warn("SLACK_ALLOW_ALL_USERS=1: every member of the channel can Choose, Edit and Send")
	}
	log.Info("starting Socket Mode listener", "config", cfg.String())
	stopRelay, err := startRelay(ctx, cfg, core, h, log)
	if err != nil {
		return err
	}
	defer stopRelay()
	return slacksurface.Serve(ctx, smc, h, log)
}

// startRelay makes the listener post Message 2 for every strategy set core publishes: it reacts to core's outbox
// (strategy_set.published) instead of waiting for `--post-run`. The returned function stops it and waits.
// SLACK_OUTBOX=off turns it off. Do not also run `--post-run --message chooser` for the same run while it is on:
// the command and the listener share no state, so Message 2 would be posted twice.
func startRelay(ctx context.Context, cfg slacksurface.Config, core *slacksurface.CoreHTTP, h *slacksurface.Handler, log *slog.Logger) (func(), error) {
	if cfg.RelayOff {
		log.Warn("SLACK_OUTBOX=off: Message 2 is not posted when core publishes a strategy set")
		return func() {}, nil
	}
	relay, err := slacksurface.NewRelay(core, h.Publisher(), log, cfg.RelayPoll)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()
	log.Info("reacting to core's strategy_set.published events")
	return func() {
		cancel()
		<-done
	}, nil
}

// postArgs names the objects the post command publishes.
type postArgs struct{ run, account, episode, message string }

func post(ctx context.Context, p *slacksurface.Publisher, a postArgs) error {
	bi := func() error { _, err := p.PostBI(ctx, a.account); return err }
	chooser := func() error { _, err := p.PostChooser(ctx, a.run); return err }
	judgment := func() error { _, err := p.PostJudgment(ctx, a.run, a.episode); return err }
	steps := map[string][]func() error{"play": {bi, chooser}, "bi": {bi}, "chooser": {chooser}, "judgment": {judgment}}
	fns, ok := steps[a.message]
	if !ok {
		return fmt.Errorf("unknown --message %q (play, bi, chooser, judgment)", a.message)
	}
	for _, fn := range fns {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}
