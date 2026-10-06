package ask

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// playSettleTimeout is how long a confirmed play_next is waited on before the task goes on without the final status.
	playSettleTimeout = 90 * time.Second
	playPollEvery     = 2 * time.Second

	continuationFailed = "The step ran, but I could not finish the rest of the task just now. Ask me again and I will pick up from here."
)

// RunAction runs one confirmed demo action. The adapter calls it only after a human pressed Run. When a task was
// paused on this action (the person asked for more than the action), core waits for the action's status to settle and
// then resumes the task: the rest of the answer comes back as the continuation.
func (s *Service) RunAction(ctx context.Context, req ActionRequest) (ActionResult, error) {
	if !validKind(req.Kind) || req.User == "" || len(req.User) > MaxUserChars || len(req.ThreadRef) > MaxThreadRefChars ||
		(req.TurnID != "" && !turnIDRe.MatchString(req.TurnID)) {
		return ActionResult{}, fmt.Errorf("%w: kind and user are required", ErrBadRequest)
	}
	if req.TurnID == "" {
		req.TurnID = s.newID()
	}
	res, err := s.execute(ctx, req.Kind)
	if err != nil || req.ThreadRef == "" {
		return res, err
	}
	s.remember(ctx, req.ThreadRef, "I ran the step you confirmed. "+res.MessageMarkdown)
	if res.Status != "done" {
		return res, nil
	}
	task := s.takePending(ctx, req.ThreadRef, req.Kind)
	if task == nil || s.worker == nil {
		return res, nil
	}
	res.Continuation = s.resume(ctx, req, *task, res)
	return res, nil
}

// takePending returns the task paused on kind in this conversation and clears it, so a double press resumes nothing.
func (s *Service) takePending(ctx context.Context, ref, kind string) *Pending {
	p, err := s.store.Pending(ctx, ref)
	if err != nil {
		s.log.WarnContext(ctx, "ask: could not read the paused task", "error", err)
		return nil
	}
	if p == nil || p.Kind != kind || s.now().Sub(p.At) > pendingTTL {
		return nil
	}
	if err := s.store.SetPending(ctx, ref, nil); err != nil {
		s.log.WarnContext(ctx, "ask: could not clear the paused task", "error", err)
	}
	return p
}

func (s *Service) remember(ctx context.Context, ref, text string) {
	if err := s.store.AppendTurns(ctx, ref, Turn{Role: RoleCliff, Text: clipRunes(text, maxHistoryText), At: s.now()}); err != nil {
		s.log.WarnContext(ctx, "ask: could not store the turn", "error", err)
	}
}

// resume finishes a paused task: it waits for a played event's pipeline to settle, then asks the worker to continue
// with the action's result in front of it. A failure here never undoes or hides the action that ran.
func (s *Service) resume(ctx context.Context, req ActionRequest, p Pending, res ActionResult) *Answer {
	s.progress.start(req.TurnID)
	if req.Kind == ActionPlayNext {
		s.progress.add(req.TurnID, "Waiting for the pipeline to finish…")
		s.awaitPipeline(ctx, req.TurnID)
	}
	text := "The person confirmed the step you proposed and it ran. Result: " + res.MessageMarkdown +
		"\nContinue their original request from the data, now that the step is done, and finish it. Original request: " + p.Question
	ans, err := s.respond(ctx, turn{workerText: clipRunes(text, MaxTextChars), question: p.Question + " (continued after the step ran)",
		channelKind: p.ChannelKind, ref: req.ThreadRef, turnID: req.TurnID})
	if err != nil {
		s.log.ErrorContext(ctx, "ask: could not resume the task", "error", err)
		s.progress.finish(req.TurnID)
		return &Answer{AnswerMarkdown: continuationFailed, Citations: []Citation{}}
	}
	return &ans
}

// awaitPipeline polls the manifest's progress until the pipeline is complete or failed, adding a line as stages finish.
func (s *Service) awaitPipeline(ctx context.Context, turnID string) {
	path, ok := s.manifestPath(progressPath)
	if !ok {
		return
	}
	deadline := s.now().Add(playSettleTimeout)
	seen := map[string]bool{}
	for {
		if overall := s.pollStages(ctx, path, turnID, seen); overall == "complete" || overall == "failed" {
			return
		}
		if ctx.Err() != nil || !s.now().Add(playPollEvery).Before(deadline) {
			return
		}
		s.sleep(ctx, playPollEvery)
	}
}

func (s *Service) pollStages(ctx context.Context, path, turnID string, seen map[string]bool) string {
	status, body, err := s.backend.Get(ctx, path)
	if err != nil || status != 200 {
		return ""
	}
	var p struct {
		Overall string `json:"overall"`
		Stages  []struct {
			Stage  string `json:"stage"`
			Status string `json:"status"`
		} `json:"stages"`
	}
	if json.Unmarshal(body, &p) != nil {
		return ""
	}
	for _, st := range p.Stages {
		if st.Status == "running" || st.Status == "waiting" || st.Status == "not_started" || seen[st.Stage] {
			continue
		}
		seen[st.Stage] = true
		name := stageWords[st.Stage]
		if name == "" {
			name = strings.ReplaceAll(st.Stage, "_", " ")
		}
		s.progress.add(turnID, name+": "+strings.ReplaceAll(st.Status, "_", " "))
	}
	return p.Overall
}
