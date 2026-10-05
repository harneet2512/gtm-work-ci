package slacksurface

import (
	"context"
	"errors"

	"github.com/slack-go/slack"
)

// readOnlyAction reports actions that only open a modal; they take no lock and write nothing, so
// they are never delayed by a concurrent write (a trigger_id lives about three seconds).
func readOnlyAction(id string) bool {
	switch id {
	case ActionStrategyViewFull, ActionSelectedViewFull, ActionSelectedEdit, ActionJudgmentCorrect, ActionJudgmentNote:
		return true
	}
	return false
}

// blockAction handles a button press.
func (h *Handler) blockAction(cb slack.InteractionCallback) Deferred {
	acts := cb.ActionCallback.BlockActions
	if len(acts) == 0 {
		return nil
	}
	a := acts[0]
	if isLinkAction(a.ActionID) { // a URL button: Slack opens the link, nothing to do
		return nil
	}
	t, err := DecodeTarget(a.Value)
	if err != nil {
		h.log.Warn("ignoring action with a bad target", "action", a.ActionID)
		return nil
	}
	key := "act:" + cb.TriggerID + ":" + a.ActionID
	if cb.TriggerID != "" && !h.firstDelivery(key) {
		h.log.Info("duplicate delivery ignored", "action", a.ActionID)
		return nil
	}
	ch, ts := where(cb)
	t.ChannelID, t.MessageTS = ch, ts
	actor := actorOf(cb.User)
	return func(ctx context.Context) {
		if !readOnlyAction(a.ActionID) {
			release, ok := h.acquire(t.lockKey(), workTimeout)
			if !ok {
				h.forget(key)
				h.fail(ctx, ch, cb.User.ID, a.ActionID, errBusy)
				return
			}
			defer release()
		}
		if err := h.dispatchAction(ctx, a.ActionID, t, actor, cb.TriggerID); err != nil {
			h.forget(key)
			h.fail(ctx, ch, cb.User.ID, a.ActionID, err)
		}
	}
}

func (h *Handler) dispatchAction(ctx context.Context, id string, t Target, actor string, trigger string) error {
	switch id {
	case ActionStrategyViewFull, ActionSelectedViewFull:
		return h.openModal(ctx, trigger, t, func() (slack.ModalViewRequest, error) {
			rs, dir, err := h.load(ctx, t.RunID)
			if err != nil {
				return slack.ModalViewRequest{}, err
			}
			cand, ok := rs.Candidate(t.CandidateID)
			if !ok {
				return slack.ModalViewRequest{}, ErrNotFound
			}
			return RenderFullModal(cand, rs.Bundle(cand), viewOf(cand, h.decisionOrNil(ctx, t.RunID), dir), t), nil
		})
	case ActionSelectedEdit:
		return h.openModal(ctx, trigger, t, func() (slack.ModalViewRequest, error) {
			rs, dir, err := h.load(ctx, t.RunID)
			if err != nil {
				return slack.ModalViewRequest{}, err
			}
			dec, err := h.core.GetStrategyDecision(ctx, t.RunID)
			if err != nil {
				return slack.ModalViewRequest{}, err
			}
			cand, ok := rs.Candidate(dec.SelectedCandidateID)
			if !ok {
				return slack.ModalViewRequest{}, ErrNotFound
			}
			t.CandidateID = cand.CandidateID
			return RenderEditModal(viewOf(cand, &dec, dir), t), nil
		})
	case ActionStrategyChoose:
		return h.choose(ctx, t, actor)
	case ActionSelectedSend:
		return h.send(ctx, t, actor)
	case ActionJudgmentConfirm:
		return h.verdict(ctx, t, VerdictRequest{Verdict: VerdictConfirmed, Surface: SurfaceSlack, ActorLabel: actor})
	case ActionJudgmentCorrect:
		return h.openModal(ctx, trigger, t, func() (slack.ModalViewRequest, error) {
			inf, err := h.core.GetJudgmentInference(ctx, t.EpisodeID)
			return RenderCorrectionModal(inf, t), err
		})
	case ActionJudgmentNote:
		_, err := h.poster.OpenView(ctx, trigger, RenderNoteModal(t))
		return err
	}
	h.log.Warn("unknown action", "action", id)
	return nil
}

// load reads the run's strategies and the people needed to show recipients.
func (h *Handler) load(ctx context.Context, runID string) (RunStrategies, Directory, error) {
	rs, err := h.core.GetStrategies(ctx, runID)
	if err != nil {
		return RunStrategies{}, nil, err
	}
	dir, err := h.core.People(ctx, rs.StrategySet.AccountID)
	if err != nil {
		return RunStrategies{}, nil, err
	}
	return rs, dir, nil
}

// decisionOrNil returns core's decision for the run, or nil when none exists yet.
func (h *Handler) decisionOrNil(ctx context.Context, runID string) *HumanStrategyDecision {
	dec, err := h.core.GetStrategyDecision(ctx, runID)
	if err != nil {
		return nil
	}
	return &dec
}

// openModal opens a loading modal at once, while the trigger_id is fresh, then fills it in from
// core. Opening never waits on core, so a slow core cannot expire the trigger.
func (h *Handler) openModal(ctx context.Context, trigger string, t Target, build func() (slack.ModalViewRequest, error)) error {
	viewID, err := h.poster.OpenView(ctx, trigger, LoadingModal(t))
	if err != nil {
		return err
	}
	v, err := build()
	if err != nil {
		if uerr := h.poster.UpdateView(ctx, viewID, ErrorModal(t)); uerr != nil {
			h.log.ErrorContext(ctx, "could not show the error modal", "error", uerr)
		}
		return err
	}
	return h.poster.UpdateView(ctx, viewID, v)
}

// existing returns the decision a 409 refers to: the record it carried, else core's current one.
func (h *Handler) existing(ctx context.Context, runID string, err error) (HumanStrategyDecision, *ConflictError, error) {
	var ce *ConflictError
	if !errors.As(err, &ce) {
		return HumanStrategyDecision{}, nil, err
	}
	if ce.Decision != nil {
		return *ce.Decision, ce, nil
	}
	dec, gerr := h.core.GetStrategyDecision(ctx, runID)
	return dec, ce, gerr
}

// choose records the human's choice and rewrites the chooser into the selected action in place.
// A repeat with the same candidate returns the existing decision; a different candidate gets 409
// selection_locked, and either way the message is re-rendered from the returned record.
func (h *Handler) choose(ctx context.Context, t Target, actor string) error {
	dec, err := h.core.RecordStrategyDecision(ctx, t.RunID, StrategyDecisionRequest{
		SelectedCandidateID: t.CandidateID, Surface: SurfaceSlack, ActorLabel: actor,
	})
	if err != nil {
		var amb *AmbiguousError
		if errors.As(err, &amb) {
			return h.refreshUnknown(ctx, t)
		}
		// selection_locked or already_decided: the message must show what core holds, which is the
		// earlier choice, not the one that was just clicked.
		if dec, _, err = h.existing(ctx, t.RunID, err); err != nil {
			return err
		}
	}
	return h.renderSelected(ctx, t.RunID, dec, t.ChannelID, t.MessageTS, "")
}

func (h *Handler) renderSelected(ctx context.Context, runID string, dec HumanStrategyDecision, ch, ts, refusal string) error {
	rs, dir, err := h.load(ctx, runID)
	if err != nil {
		return slackSideError{err}
	}
	msg, err := RenderSelected(rs, dec, dir, refusal, h.pub.webURL)
	if err != nil {
		return slackSideError{err}
	}
	if err := h.poster.UpdateMessage(ctx, ch, ts, msg); err != nil {
		return slackSideError{err}
	}
	return nil
}

// send makes the explicit Send, updates Message 2, then posts Message 3 once core has the inference.
func (h *Handler) send(ctx context.Context, t Target, actor string) error {
	dec, err := h.core.SendRun(ctx, t.RunID, SendRequest{Decision: SendSend, Surface: SurfaceSlack, ActorLabel: actor})
	var refused *RefusedError
	switch {
	case errors.As(err, &refused): // policy refused: show the reason in place
		cur, gerr := h.core.GetStrategyDecision(ctx, t.RunID)
		if gerr != nil {
			return gerr
		}
		if rerr := h.renderSelected(ctx, t.RunID, cur, t.ChannelID, t.MessageTS, refused.Message); rerr != nil {
			return rerr
		}
		return errNotSent
	case errors.Is(err, ErrConflict): // already decided: show the existing record, never a second Message 3
		existing, _, eerr := h.existing(ctx, t.RunID, err)
		if errors.Is(eerr, ErrNotFound) { // no_choice: nothing was chosen, so nothing could be sent
			return errNotSent
		}
		if eerr != nil {
			return eerr
		}
		if rerr := h.renderSelected(ctx, t.RunID, existing, t.ChannelID, t.MessageTS, ""); rerr != nil {
			return rerr
		}
		if existing.SendDecision != SendSend {
			return errNotSent
		}
		return nil
	case err != nil:
		var amb *AmbiguousError
		if errors.As(err, &amb) {
			return h.refreshUnknown(ctx, t)
		}
		return err
	}
	if err := h.renderSelected(ctx, t.RunID, dec, t.ChannelID, t.MessageTS, ""); err != nil {
		return err
	}
	if _, err := h.pub.PostJudgment(ctx, t.RunID, dec.DecisionEpisodeID); err != nil {
		if errors.Is(err, ErrNotReady) || errors.Is(err, ErrNotFound) {
			h.startJudgmentPoll(t.RunID, dec.DecisionEpisodeID)
			return nil
		}
		return slackSideError{err}
	}
	return nil
}

// refreshUnknown handles a Choose or Send whose outcome is unknown: it re-reads core's decision and
// re-renders the message from it, then reports "status unknown" to the user. It never claims that
// nothing changed.
func (h *Handler) refreshUnknown(ctx context.Context, t Target) error {
	if dec, err := h.core.GetStrategyDecision(ctx, t.RunID); err == nil {
		_ = h.renderSelected(ctx, t.RunID, dec, t.ChannelID, t.MessageTS, "")
	}
	return errUnknown
}

// verdict submits a confirm/correct/note to core and re-renders Message 3 in place.
func (h *Handler) verdict(ctx context.Context, t Target, req VerdictRequest) error {
	inf, err := h.submitVerdict(ctx, t, req)
	if err != nil {
		return err
	}
	return h.renderJudgment(ctx, t, inf)
}

func (h *Handler) submitVerdict(ctx context.Context, t Target, req VerdictRequest) (JudgmentInference, error) {
	inf, err := h.core.SubmitJudgmentVerdict(ctx, t.EpisodeID, req)
	if errors.Is(err, ErrConflict) {
		inf, err = h.core.GetJudgmentInference(ctx, t.EpisodeID)
	}
	return inf, err
}

func (h *Handler) renderJudgment(ctx context.Context, t Target, inf JudgmentInference) error {
	rs, err := h.core.GetStrategies(ctx, t.RunID)
	if err != nil {
		return slackSideError{err}
	}
	if err := h.poster.UpdateMessage(ctx, t.ChannelID, t.MessageTS, RenderJudgment(inf, rs, t.RunID, h.pub.webURL)); err != nil {
		return slackSideError{err}
	}
	return nil
}
