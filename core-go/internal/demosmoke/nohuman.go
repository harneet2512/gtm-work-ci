package demosmoke

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/fakecore"
	"github.com/harneet2512/gtm-work/core-go/internal/slackfake"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

const (
	smokeChannel = "C0SMOKE"
	runTimeout   = 90 * time.Second
	stepTimeout  = 15 * time.Second
	smokeUser    = "U1"
)

// RunNoHuman drives the whole demo flow without Slack: the real Slack surface (Socket Mode client, handler,
// publisher) runs against a fake Slack and the fake core, and the driver pushes the interactions a person
// would click:
//
//	post M1 and M2, View full, Choose B, Edit (open, submit), Send, (M3 posts by itself), Needs correction (submit)
//
// It writes the run's artefacts to dir (the fakecore request log and the audit log of the bot's Slack
// writes) for Verify. Nothing here touches the network beyond loopback or reads a credential.
func RunNoHuman(ctx context.Context, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("demosmoke: %w", err)
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	core, err := fakecore.New(fakecore.Options{Token: token, LogPath: filepath.Join(dir, CoreLogFile)})
	if err != nil {
		return err
	}
	coreSrv := httptest.NewServer(core.Handler())
	defer coreSrv.Close()
	fs := slackfake.New()
	defer fs.Close()
	audit, err := os.OpenFile(filepath.Join(dir, SlackAuditFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("demosmoke: %w", err)
	}
	defer audit.Close()

	cfg := slacksurface.Config{AppToken: "xapp-fake-smoke", BotToken: "xoxb-fake-smoke", ChannelID: smokeChannel}
	api, smc := slacksurface.NewSlackClients(cfg, fs.APIURL())
	poster := slacksurface.NewAuditPoster(slacksurface.NewSlackPoster(api), audit)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := slacksurface.NewHandler(slacksurface.NewCoreHTTP(coreSrv.URL, slacksurface.Secret(token), nil), poster, cfg.ChannelID, "https://ghost.example.test", quiet)
	served := make(chan error, 1)
	go func() { served <- slacksurface.Serve(ctx, smc, h, quiet) }()
	d := &driver{ctx: ctx, fs: fs, h: h}
	runErr := d.play()
	cancel()
	if err := <-served; err != nil && runErr == nil && ctx.Err() == nil {
		runErr = fmt.Errorf("demosmoke: socket mode: %w", err)
	}
	return runErr
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("demosmoke: %w", err)
	}
	return hex.EncodeToString(b), nil
}

type driver struct {
	ctx context.Context
	fs  *slackfake.Server
	h   *slacksurface.Handler
}

func (d *driver) step(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(d.ctx, timeout)
}

func (d *driver) click(action, trigger string, ts string, t slacksurface.Target) error {
	ctx, cancel := d.step(stepTimeout)
	defer cancel()
	_, err := d.fs.Push(ctx, map[string]any{
		"type": "block_actions", "trigger_id": trigger,
		"user":      map[string]any{"id": smokeUser, "name": "alex"},
		"channel":   map[string]any{"id": smokeChannel},
		"container": map[string]any{"type": "message", "channel_id": smokeChannel, "message_ts": ts},
		"actions":   []any{map[string]any{"action_id": action, "block_id": "b", "type": "button", "value": t.Encode()}},
	})
	return err
}

func (d *driver) submit(viewID, callback string, meta slacksurface.Target, values map[string]string) error {
	state := map[string]any{}
	for block, v := range values {
		state[block] = map[string]any{slacksurface.InputAction: map[string]any{"type": "plain_text_input", "value": v}}
	}
	ctx, cancel := d.step(stepTimeout)
	defer cancel()
	_, err := d.fs.Push(ctx, map[string]any{
		"type": "view_submission", "user": map[string]any{"id": smokeUser, "name": "alex"},
		"view": map[string]any{"id": viewID, "callback_id": callback, "private_metadata": meta.Encode(),
			"state": map[string]any{"values": state}},
	})
	return err
}

func (d *driver) waitFor(method string, n int) ([]slackfake.Call, error) {
	ctx, cancel := d.step(stepTimeout)
	defer cancel()
	return d.fs.WaitCalls(ctx, method, n)
}

// play runs the flow; the first failed step ends it.
func (d *driver) play() error {
	ctx, cancel := d.step(stepTimeout)
	err := d.fs.WaitConnected(ctx)
	cancel()
	if err != nil {
		return err
	}
	pub := d.h.Publisher()
	if _, err := pub.PostBI(d.ctx, slacksurface.FixtureAccountID); err != nil {
		return fmt.Errorf("demosmoke: post M1: %w", err)
	}
	m2, err := pub.PostChooser(d.ctx, slacksurface.FixtureRunID)
	if err != nil {
		return fmt.Errorf("demosmoke: post M2: %w", err)
	}
	run := slacksurface.Target{RunID: slacksurface.FixtureRunID}
	candB := slacksurface.Target{RunID: slacksurface.FixtureRunID, CandidateID: fixtureCandidateB}
	meta := slacksurface.Target{RunID: slacksurface.FixtureRunID, CandidateID: fixtureCandidateB, ChannelID: smokeChannel, MessageTS: m2.TS}
	steps := []struct {
		name string
		do   func() error
	}{
		{"View full", func() error { return d.viewFull(m2.TS, candB) }},
		{"Choose B", func() error { return d.chooseB(m2.TS, candB) }},
		{"Edit (open)", func() error { return d.editOpen(m2.TS, run) }},
		{"Edit (submit)", func() error { return d.editSubmit(meta) }},
		{"Send", func() error { return d.send(m2.TS, run) }},
		{"Needs correction", func() error { return d.correct() }},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			return fmt.Errorf("demosmoke: %s: %w", s.name, err)
		}
	}
	return nil
}

// fixtureCandidateB is the candidate the human picks: ranked second, so Ghost's preference is overridden.
const fixtureCandidateB = "0ca00000-0000-4000-8000-0000000000a2"

func (d *driver) viewFull(ts string, t slacksurface.Target) error {
	if err := d.click(slacksurface.ActionStrategyViewFull, "T-view", ts, t); err != nil {
		return err
	}
	_, err := d.waitFor("views.open", 1)
	return err
}

func (d *driver) chooseB(ts string, t slacksurface.Target) error {
	if err := d.click(slacksurface.ActionStrategyChoose, "T-choose", ts, t); err != nil {
		return err
	}
	_, err := d.waitFor("chat.update", 1)
	return err
}

func (d *driver) editOpen(ts string, t slacksurface.Target) error {
	if err := d.click(slacksurface.ActionSelectedEdit, "T-edit", ts, t); err != nil {
		return err
	}
	_, err := d.waitFor("views.open", 2)
	return err
}

func (d *driver) editSubmit(meta slacksurface.Target) error {
	if err := d.submit("V-edit", slacksurface.CallbackEditModal, meta, map[string]string{
		slacksurface.InputTo: "Marco <marco@acme.example.test>", slacksurface.InputCC: "priya@acme.example.test",
		slacksurface.InputSubject: "Edited subject", slacksurface.InputBody: "Edited body",
	}); err != nil {
		return err
	}
	_, err := d.waitFor("chat.update", 2)
	return err
}

func (d *driver) send(ts string, t slacksurface.Target) error {
	if err := d.click(slacksurface.ActionSelectedSend, "T-send", ts, t); err != nil {
		return err
	}
	_, err := d.waitFor("chat.update", 3)
	return err
}

// correct waits for M3, which the listener posts by itself after Send, then answers it with a correction.
func (d *driver) correct() error {
	posts, err := d.waitFor("chat.postMessage", 3)
	if err != nil {
		return fmt.Errorf("M3 was not posted after Send: %w", err)
	}
	m3 := posts[2].TS
	episode := slacksurface.Target{EpisodeID: slacksurface.FixtureEpisodeID, RunID: slacksurface.FixtureRunID}
	if err := d.click(slacksurface.ActionJudgmentCorrect, "T-correct", m3, episode); err != nil {
		return err
	}
	if _, err := d.waitFor("views.open", 3); err != nil {
		return err
	}
	episode.ChannelID, episode.MessageTS = smokeChannel, m3
	if err := d.submit("V-correct", slacksurface.CallbackCorrectionModal, episode, map[string]string{
		slacksurface.InputCorrection: "The sponsor owns the timing", slacksurface.InputNote: "scripted smoke correction",
	}); err != nil {
		return err
	}
	_, err = d.waitFor("chat.update", 4)
	return err
}
