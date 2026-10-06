package ask

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
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
}

// New builds the service.
func New(worker Worker, backend Backend, signer *Signer, cfg Config, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	links := NewLinks(cfg.WebBase)
	s := &Service{worker: worker, backend: backend, signer: signer, links: links, manifestID: strings.TrimSpace(cfg.ManifestID),
		log: log, now: time.Now}
	s.tools = &Tools{B: backend, L: links, Clock: func() time.Time { return s.now() }}
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
	case s.worker == nil:
		return Answer{}, ErrUnavailable
	}
	token, err := s.signer.Issue(s.now())
	if err != nil {
		return Answer{}, err
	}
	wa, err := s.worker.Ask(ctx, WorkerRequest{Text: text, ChannelKind: req.ChannelKind, AskToken: token})
	if err != nil {
		return Answer{}, err
	}
	s.log.InfoContext(ctx, "ask answered", "tool_calls", wa.ToolCalls, "dont_know", wa.DontKnow, "timed_out", wa.TimedOut,
		"model", wa.Model, "channel_kind", req.ChannelKind, "thread_ref", req.ThreadRef)
	return s.compose(ctx, wa), nil
}

// compose turns the worker's answer into the one core returns: checked citations, the dry-run label, a proposed action
// described by core itself, and the deep links at the end.
func (s *Service) compose(ctx context.Context, wa WorkerAnswer) Answer {
	cites := s.checkedCitations(wa.Citations)
	// The [t1] markers tie a sentence to its tool call for the worker's grounding check; a person reads the links instead.
	body := strings.TrimSpace(citeMarker.ReplaceAllString(wa.AnswerMarkdown, ""))
	if body == "" {
		body = "I don't know. The data I can read does not show that."
	}
	body = labelDryRun(body, cites)
	out := Answer{Citations: cites}
	if a := wa.ProposedAction; a != nil && validKind(a.Kind) {
		// The model's own summary is never shown as what will run: core describes the action from its kind and the replay.
		out.ProposedAction = &ProposedAction{Kind: a.Kind, Summary: s.describe(ctx, a.Kind), RequiresConfirmation: true}
	}
	out.AnswerMarkdown = clipRunes(body+linksTail(cites), MaxAnswerChars)
	return out
}

func validKind(k string) bool { return k == ActionPlayNext || k == ActionDemoStatus }

// describe is the exact action a confirmation shows: a fixed sentence per kind, with the account and event number read
// from the replay when they can be (it falls back to the plain sentence, never to model text).
func (s *Service) describe(ctx context.Context, kind string) string {
	if kind == ActionDemoStatus {
		return "Show the replay status of the demo case (read only)"
	}
	plain := "Play the next event of the demo case"
	path, ok := s.manifestPath("/replay/manifests/%s/episodes")
	if !ok {
		return plain
	}
	status, body, err := s.backend.Get(ctx, path)
	if err != nil || status != 200 {
		return plain
	}
	var v struct {
		AccountID string `json:"account_id"`
		Episode   int    `json:"episode"`
		Total     int    `json:"total"`
	}
	if json.Unmarshal(body, &v) != nil || v.Total == 0 {
		return plain
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
		return fmt.Sprintf("Play the next event of the demo case (event %d of %d)", v.Episode+1, v.Total)
	}
	return fmt.Sprintf("Play the next event for %s (event %d of %d)", name, v.Episode+1, v.Total)
}

func clipRunes(s string, n int) string {
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

// RunTool serves one tool call of an ask. The token is the one Ask minted.
func (s *Service) RunTool(ctx context.Context, token, tool string, args map[string]any) (ToolResult, error) {
	if err := s.signer.Verify(token, s.now()); err != nil {
		return ToolResult{}, err
	}
	return s.tools.Run(ctx, tool, args)
}
