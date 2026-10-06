package ask

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Config is what the service needs besides its collaborators.
type Config struct {
	WebBase    string
	ManifestID string
}

var citeMarker = regexp.MustCompile(` ?\[t\d{1,2}\]`)

// Service is Ask Cliff.
type Service struct {
	worker     Worker
	backend    Backend
	signer     *Signer
	tools      *Tools
	links      Links
	manifestID string
	log        *slog.Logger
	now        func() time.Time
	store      Store
	progress   *progressBook
	newID      func() string
	sleep      func(context.Context, time.Duration)

	mu   sync.Mutex
	runs map[string]*liveRun // by ask token: the asks in flight
}

// liveRun ties an ask in flight to its progress entry and its trace steps.
type liveRun struct {
	turnID string
	run    *stepLog
}

// Option adjusts a Service.
type Option func(*Service)

// WithStore keeps conversations, paused tasks and traces in store (the default is in memory).
func WithStore(st Store) Option { return func(s *Service) { s.store = st } }

// New builds the service.
func New(worker Worker, backend Backend, signer *Signer, cfg Config, log *slog.Logger, opts ...Option) *Service {
	if log == nil {
		log = slog.Default()
	}
	links := NewLinks(cfg.WebBase)
	s := &Service{worker: worker, backend: backend, signer: signer, links: links, manifestID: strings.TrimSpace(cfg.ManifestID),
		log: log, now: time.Now, store: NewMemoryStore(), newID: newTraceID, runs: map[string]*liveRun{},
		sleep: func(ctx context.Context, d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		}}
	s.progress = newProgressBook(func() time.Time { return s.now() })
	s.tools = &Tools{B: backend, L: links, Clock: func() time.Time { return s.now() }}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Ask answers one question. Nothing state-changing runs here: a demo action comes back as a proposal.
func (s *Service) Ask(ctx context.Context, req Request) (Answer, error) {
	text := strings.TrimSpace(req.Text)
	switch {
	case text == "" || len([]rune(text)) > MaxTextChars:
		return Answer{}, fmt.Errorf("%w: text must be 1 to %d characters", ErrBadRequest, MaxTextChars)
	case req.ChannelKind != ChannelDM && req.ChannelKind != ChannelThread:
		return Answer{}, fmt.Errorf("%w: channel_kind must be dm or thread", ErrBadRequest)
	case req.User == "" || len(req.User) > MaxUserChars:
		return Answer{}, fmt.Errorf("%w: user is required", ErrBadRequest)
	case len(req.ThreadRef) > MaxThreadRefChars:
		return Answer{}, fmt.Errorf("%w: thread_ref is too long", ErrBadRequest)
	case req.TurnID != "" && !turnIDRe.MatchString(req.TurnID):
		return Answer{}, fmt.Errorf("%w: turn_id must be 8 to 64 letters, digits, - or _", ErrBadRequest)
	case s.worker == nil:
		return Answer{}, ErrUnavailable
	}
	return s.respond(ctx, turn{workerText: text, question: text, userTurn: text, channelKind: req.ChannelKind,
		ref: req.ThreadRef, turnID: req.TurnID})
}

// turn is one piece of work for the worker: a question, or the rest of a task after a confirmed action.
type turn struct {
	workerText  string // what the worker is asked
	question    string // what the person asked, shown in the trace
	userTurn    string // stored as the person's turn; "" when there is none to store (a continuation)
	channelKind string
	ref         string // the conversation; "" has no memory
	turnID      string
}

// respond runs one turn: loads the conversation, lets the worker work while core serves its tools, composes the
// answer, and records the turns, the paused task and the trace.
func (s *Service) respond(ctx context.Context, t turn) (Answer, error) {
	if t.turnID == "" {
		t.turnID = s.newID()
	}
	s.progress.start(t.turnID)
	defer s.progress.finish(t.turnID)
	history := s.history(ctx, t.ref)
	token, err := s.signer.Issue(s.now())
	if err != nil {
		return Answer{}, err
	}
	live := &liveRun{turnID: t.turnID, run: &stepLog{}}
	s.enter(token, live)
	defer s.leave(token)
	started := s.now()
	wa, err := s.worker.Ask(ctx, WorkerRequest{Text: t.workerText, ChannelKind: t.channelKind, AskToken: token, History: history})
	if err != nil {
		return Answer{}, err
	}
	traceID := s.newID()
	ans, body := s.compose(ctx, wa, s.links.Trace(traceID))
	ans.TraceID, ans.Steps, ans.TimedOut = traceID, wa.ToolCalls, wa.TimedOut
	if u := s.links.Trace(traceID); u != "" {
		ans.TraceURL = &u
	}
	s.log.InfoContext(ctx, "ask answered", "tool_calls", wa.ToolCalls, "dont_know", wa.DontKnow, "timed_out", wa.TimedOut,
		"model", wa.Model, "channel_kind", t.channelKind, "thread_ref", t.ref, "history_turns", len(history))
	s.record(ctx, t, ans, body, wa, live.run, started)
	return ans, nil
}

func (s *Service) enter(token string, r *liveRun) {
	s.mu.Lock()
	s.runs[token] = r
	s.mu.Unlock()
}

func (s *Service) leave(token string) {
	s.mu.Lock()
	delete(s.runs, token)
	s.mu.Unlock()
}

func (s *Service) live(token string) *liveRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[token]
}

// history is what the worker is shown of the conversation so far. A store failure costs the memory, not the answer.
func (s *Service) history(ctx context.Context, ref string) []HistoryTurn {
	if ref == "" {
		return nil
	}
	turns, err := s.store.Turns(ctx, ref, MaxStoredTurns)
	if err != nil {
		s.log.WarnContext(ctx, "ask: could not load the conversation", "error", err)
		return nil
	}
	return BuildHistory(turns)
}

// record keeps what this turn leaves behind: the conversation, the task paused on an action, and the trace.
func (s *Service) record(ctx context.Context, t turn, ans Answer, body string, wa WorkerAnswer, r *stepLog, started time.Time) {
	at := s.now()
	trace := Trace{ID: ans.TraceID, ThreadRef: t.ref, Question: t.question, AnswerMarkdown: ans.AnswerMarkdown, CreatedAt: at,
		Model: wa.Model, Steps: r.snapshot(), DurationMS: at.Sub(started).Milliseconds(), TimedOut: wa.TimedOut}
	if u := wa.Usage; u != nil {
		trace.TokensIn, trace.TokensOut, trace.CostUSD = u.InputTokens, u.OutputTokens, u.CostUSD
	}
	if err := s.store.SaveTrace(ctx, trace); err != nil {
		s.log.WarnContext(ctx, "ask: could not save the trace", "error", err)
	}
	if t.ref == "" {
		return
	}
	said := body
	var pending *Pending
	if p := ans.ProposedAction; p != nil {
		said += "\n[Proposed, waiting for the person to confirm: " + p.Summary + "]"
		pending = &Pending{Kind: p.Kind, Question: t.question, ChannelKind: t.channelKind, At: at}
	}
	turns := []Turn{{Role: RoleCliff, Text: said, At: at}}
	if t.userTurn != "" {
		turns = append([]Turn{{Role: RoleUser, Text: t.userTurn, At: at}}, turns...)
	}
	if err := s.store.AppendTurns(ctx, t.ref, turns...); err != nil {
		s.log.WarnContext(ctx, "ask: could not store the turns", "error", err)
	}
	if err := s.store.SetPending(ctx, t.ref, pending); err != nil {
		s.log.WarnContext(ctx, "ask: could not store the paused task", "error", err)
	}
}

// Trace returns the trace of one answer.
func (s *Service) Trace(ctx context.Context, id string) (Trace, error) {
	if id == "" || len(id) > 64 {
		return Trace{}, fmt.Errorf("%w: trace id", ErrBadRequest)
	}
	return s.store.Trace(ctx, id)
}

// compose turns the worker's answer into the one core returns: checked citations, the dry-run label, no claim that
// knowledge influenced anything, a proposed action described by core itself, the deep links and the trace link at
// the end. It also returns the body alone, which is what the conversation remembers.
func (s *Service) compose(ctx context.Context, wa WorkerAnswer, traceURL string) (Answer, string) {
	cites := s.checkedCitations(wa.Citations)
	// The [t1] markers tie a sentence to its tool call for the worker's grounding check; a person reads the links instead.
	body := strings.TrimSpace(citeMarker.ReplaceAllString(wa.AnswerMarkdown, ""))
	if body == "" {
		body = "I don't know. The data I can read does not show that."
	}
	body, _ = withoutInfluenceClaims(body)
	body = labelDryRun(body, cites)
	out := Answer{Citations: cites}
	if a := wa.ProposedAction; a != nil && validKind(a.Kind) {
		// The model's own summary is never shown as what will run: core describes the action from its kind and the replay.
		summary, target := s.describe(ctx, a.Kind)
		out.ProposedAction = &ProposedAction{Kind: a.Kind, Summary: summary, Target: target, RequiresConfirmation: true}
	}
	tail := linksTail(cites)
	if traceURL != "" {
		tail += "\n\nTrace: [how I got this](" + traceURL + ")"
	}
	out.AnswerMarkdown = clipRunes(body, MaxAnswerChars-len([]rune(tail))) + tail
	return out, body
}

func validKind(k string) bool { return k == ActionPlayNext || k == ActionDemoStatus }

// describe is the exact action a confirmation shows, from its kind and the replay and never from model text: a
// sentence, and the target it acts on ("MedTech Advances (event 13 of 13)", or "" when the replay cannot be read).
func (s *Service) describe(ctx context.Context, kind string) (summary, target string) {
	if kind == ActionDemoStatus {
		return "Show the replay status of the demo case (read only)", "the demo case"
	}
	plain := "Play the next event of the demo case"
	path, ok := s.manifestPath("/replay/manifests/%s/episodes")
	if !ok {
		return plain, "the demo case"
	}
	status, body, err := s.backend.Get(ctx, path)
	if err != nil || status != 200 {
		return plain, "the demo case"
	}
	var v struct {
		AccountID string `json:"account_id"`
		Episode   int    `json:"episode"`
		Total     int    `json:"total"`
	}
	if json.Unmarshal(body, &v) != nil || v.Total == 0 {
		return plain, "the demo case"
	}
	name := ""
	if status, body, err = s.backend.Get(ctx, "/accounts/"+url.PathEscape(v.AccountID)); err == nil && status == 200 {
		var a struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(body, &a)
		name = a.Name
	}
	if name == "" {
		return fmt.Sprintf("Play the next event of the demo case (event %d of %d)", v.Episode+1, v.Total),
			fmt.Sprintf("the demo case (event %d of %d)", v.Episode+1, v.Total)
	}
	return fmt.Sprintf("Play the next event for %s (event %d of %d)", name, v.Episode+1, v.Total),
		fmt.Sprintf("%s (event %d of %d)", name, v.Episode+1, v.Total)
}

func clipRunes(s string, n int) string {
	if n < 1 {
		return ""
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// checkedCitations keeps at most MaxCitations and drops any link that does not point into the control plane.
func (s *Service) checkedCitations(in []WorkerCitation) []Citation {
	out := make([]Citation, 0, len(in))
	for _, c := range in {
		if c.CallID == "" || c.Tool == "" || len(out) >= MaxCitations {
			continue
		}
		label := strings.TrimSpace(c.Label)
		if label == "" {
			label = c.Tool
		}
		cite := Citation{CallID: c.CallID, Tool: c.Tool, Label: clipRunes(label, 200)}
		if c.URL != nil && s.links.Owns(*c.URL) {
			u := *c.URL
			cite.URL = &u
		}
		out = append(out, cite)
	}
	return out
}

// labelDryRun puts DRAFT or PREVIEW in front of an answer that rests on a dry-run tool.
func labelDryRun(body string, cites []Citation) string {
	for _, c := range cites {
		switch {
		case c.Tool == "draft_followup" && !strings.Contains(body, "DRAFT"):
			return "**" + DraftLabel + "**\n\n" + body
		case c.Tool == "crm_update_preview" && !strings.Contains(body, "PREVIEW"):
			return "**" + PreviewLabel + "**\n\n" + body
		}
	}
	return body
}

// linksTail ends the answer with the distinct deep links of its citations.
func linksTail(cites []Citation) string {
	var parts []string
	seen := map[string]bool{}
	for _, c := range cites {
		if c.URL == nil || seen[*c.URL] {
			continue
		}
		seen[*c.URL] = true
		parts = append(parts, fmt.Sprintf("[%s](%s)", c.Label, *c.URL))
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n\nOpen in gtm_ai: " + strings.Join(parts, " · ")
}

// RunTool serves one tool call of an ask. The token is the one Ask minted. The call is worded as a progress line
// for the person waiting and recorded in the answer's trace.
func (s *Service) RunTool(ctx context.Context, token, tool string, args map[string]any) (ToolResult, error) {
	if err := s.signer.Verify(token, s.now()); err != nil {
		return ToolResult{}, err
	}
	live := s.live(token)
	if live != nil {
		s.progress.add(live.turnID, stepLine(tool, args))
	}
	res, err := s.tools.Run(ctx, tool, args)
	if live != nil {
		if err != nil {
			live.run.failed(tool, args, err)
		} else {
			live.run.add(tool, args, res)
		}
	}
	return res, err
}
