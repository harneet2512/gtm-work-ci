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
//	post M1 (with View trace) and M2, View full, Select B, Edit (open, submit), Send, (M3 posts by itself), then
//	either Edit interpretation (open, submit) or Don't learn this, as the Answer says
//
// It writes the run's artefacts to dir (the fakecore request log and the audit log of the bot's Slack
// writes) for Verify. Nothing here touches the network beyond loopback or reads a credential.
func RunNoHuman(ctx context.Context, dir string) error {
	return RunNoHumanAnswering(ctx, dir, AnswerEditInterpretation)
}

// Answer is how the driver answers Message 3, Cliff's learning interpretation.
type Answer string

const (
	// AnswerEditInterpretation opens Edit interpretation and saves a corrected interpretation with a note.
	AnswerEditInterpretation Answer = "edit-interpretation"
	// AnswerNoLearning clicks Don't learn this: an explicit no-learning verdict.
	AnswerNoLearning Answer = "no-learning"
	// AnswerCorrect clicks Correct: the interpretation is right, an explicit confirmed verdict.
	AnswerCorrect Answer = "correct"
)

// RunNoHumanAnswering is RunNoHuman answering Message 3 as given.
func RunNoHumanAnswering(ctx context.Context, dir string, answer Answer) error {
	if answer != AnswerEditInterpretation && answer != AnswerNoLearning && answer != AnswerCorrect {
		return fmt.Errorf("demosmoke: unknown Message 3 answer %q (%s, %s, %s)", answer, AnswerEditInterpretation, AnswerNoLearning, AnswerCorrect)
	}
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
	d := &driver{ctx: ctx, fs: fs, h: h, answer: answer}
	runErr := d.play(fixtureFlow)
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
	ctx    context.Context
	fs     *slackfake.Server
	h      *slacksurface.Handler
	answer Answer // how Message 3 is answered; empty means AnswerEditInterpretation
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
func (d *driver) play(f flow) error {
	ctx, cancel := d.step(stepTimeout)
	err := d.fs.WaitConnected(ctx)
	cancel()
	if err != nil {
		return err
	}
	pub := d.h.Publisher()
	if _, err := pub.PostBIForEpisode(d.ctx, f.accountID, f.episodeID); err != nil {
		return fmt.Errorf("demosmoke: post M1: %w", err)
	}
	m2, err := pub.PostChooser(d.ctx, f.runID)
	if err != nil {
		return fmt.Errorf("demosmoke: post M2: %w", err)
	}
	run := slacksurface.Target{RunID: f.runID}
	candB := slacksurface.Target{RunID: f.runID, CandidateID: f.chooseCandidate}
	meta := slacksurface.Target{RunID: f.runID, CandidateID: f.chooseCandidate, ChannelID: smokeChannel, MessageTS: m2.TS}
	answerName, answer := "Edit interpretation (submit)", func() error { return d.correct(f) }
	switch d.answer {
	case AnswerNoLearning:
		answerName, answer = "Don't learn this", func() error { return d.dontLearn(f) }
	case AnswerCorrect:
		answerName, answer = "Correct", func() error { return d.confirm(f) }
	}
	steps := []struct {
		name string
		do   func() error
	}{
		{"View full", func() error { return d.viewFull(m2.TS, candB) }},
		{"Select B", func() error { return d.chooseB(m2.TS, candB) }},
		{"Edit (open)", func() error { return d.editOpen(m2.TS, run) }},
		{"Edit (submit)", func() error { return d.editSubmit(f, meta) }},
		{"Send", func() error { return d.send(m2.TS, run) }},
		{answerName, answer},
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

// flow is one episode's identifiers and the texts the driver submits — the fake-core run uses the fixture
// values, the real-core run the seeded ones. otherCandidate and blockTo are only set for the real-core
// run: the driver clicks another candidate after choosing (selection_locked must hold the first choice)
// and first edits the recipients to a person with no email (the send-time re-evaluation must block).
type flow struct {
	accountID, runID, episodeID, chooseCandidate string
	otherCandidate                               string
	blockTo                                      string
	editTo, editCC, editSubject, editBody        string
	correction, note                             string
}

// fixtureFlow is the contract fixture episode fakecore serves.
var fixtureFlow = flow{
	accountID: slacksurface.FixtureAccountID, runID: slacksurface.FixtureRunID,
	episodeID: slacksurface.FixtureEpisodeID, chooseCandidate: fixtureCandidateB,
	editTo: "Marco <marco@acme.example.test>", editCC: "priya@acme.example.test",
	editSubject: "Edited subject", editBody: "Edited body",
	correction: "The sponsor owns the timing", note: "scripted smoke correction"}

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

func (d *driver) editSubmit(f flow, meta slacksurface.Target) error {
	return d.editSubmitAt("V-edit", f, meta, 2)
}

// editSubmitAt submits the Edit modal as view viewID (the handler dedupes submissions by view id, so the
// real-core run's second edit needs its own) and waits until the message has been updated wantUpdates
// times in this run.
func (d *driver) editSubmitAt(viewID string, f flow, meta slacksurface.Target, wantUpdates int) error {
	if err := d.submit(viewID, slacksurface.CallbackEditModal, meta, map[string]string{
		slacksurface.InputTo: f.editTo, slacksurface.InputCC: f.editCC,
		slacksurface.InputSubject: f.editSubject, slacksurface.InputBody: f.editBody,
	}); err != nil {
		return err
	}
	_, err := d.waitFor("chat.update", wantUpdates)
	return err
}

func (d *driver) send(ts string, t slacksurface.Target) error {
	if err := d.click(slacksurface.ActionSelectedSend, "T-send", ts, t); err != nil {
		return err
	}
	_, err := d.waitFor("chat.update", 3)
	return err
}

// correct waits for M3, which the listener posts by itself after Send, then answers it with Edit interpretation.
func (d *driver) correct(f flow) error {
	return d.correctAt(f, 4)
}

// dontLearn waits for M3 and answers it with Don't learn this: one click, no modal, an explicit no-learning
// verdict, and M3 updated in place.
func (d *driver) dontLearn(f flow) error {
	return d.oneClick(f, slacksurface.ActionJudgmentNoLearn, "T-no-learning")
}

// confirm waits for M3 and answers it with Correct: one click, no modal, an explicit confirmed verdict, and M3
// updated in place.
func (d *driver) confirm(f flow) error {
	return d.oneClick(f, slacksurface.ActionJudgmentConfirm, "T-confirm")
}

// oneClick answers M3 with a single button and waits for the message to be updated in place.
func (d *driver) oneClick(f flow, action, trigger string) error {
	posts, err := d.waitFor("chat.postMessage", 3)
	if err != nil {
		return fmt.Errorf("M3 was not posted after Send: %w", err)
	}
	episode := slacksurface.Target{EpisodeID: f.episodeID, RunID: f.runID}
	if err := d.click(action, trigger, posts[2].TS, episode); err != nil {
		return err
	}
	_, err = d.waitFor("chat.update", 4)
	return err
}

// correctAt is correct waiting for the verdict's message update at position wantUpdates (the real-core
// run has more updates on M2 by then).
func (d *driver) correctAt(f flow, wantUpdates int) error {
	posts, err := d.waitFor("chat.postMessage", 3)
	if err != nil {
		return fmt.Errorf("M3 was not posted after Send: %w", err)
	}
	m3 := posts[2].TS
	episode := slacksurface.Target{EpisodeID: f.episodeID, RunID: f.runID}
	if err := d.click(slacksurface.ActionJudgmentCorrect, "T-correct", m3, episode); err != nil {
		return err
	}
	if _, err := d.waitFor("views.open", 3); err != nil {
		return err
	}
	episode.ChannelID, episode.MessageTS = smokeChannel, m3
	if err := d.submit("V-correct", slacksurface.CallbackCorrectionModal, episode, map[string]string{
		slacksurface.InputCorrection: f.correction, slacksurface.InputNote: f.note,
	}); err != nil {
		return err
	}
	_, err = d.waitFor("chat.update", wantUpdates)
	return err
}
