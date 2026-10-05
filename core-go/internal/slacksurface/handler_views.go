package slacksurface

import (
	"context"
	"errors"
	"net/mail"
	"slices"
	"strings"

	"github.com/slack-go/slack"
)

func inputValue(v *slack.View, block string) string {
	if v == nil || v.State == nil {
		return ""
	}
	return strings.TrimSpace(v.State.Values[block][inputAction].Value)
}

// fieldError is an inline error on one modal input.
type fieldError struct{ block, msg string }

func (e *fieldError) Error() string { return e.block + ": " + e.msg }

// parseRecipients resolves "Name <email>" / email / name entries against the account's people.
// Every entry must match a person on the account (the contract addresses people, not free text).
// role and the existing recipients preserve the "why" of a person who was already addressed.
func parseRecipients(text, role, block string, dir Directory, existing []Recipient) ([]Recipient, error) {
	out := []Recipient{} // never nil: an empty CC must be sent as [], not omitted
	for _, p := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		person, ok := dir.match(p)
		if !ok {
			return nil, &fieldError{block, "No person on this account matches: " + truncate(p, 60)}
		}
		r := Recipient{PersonID: person.ID, Role: role}
		for _, e := range existing {
			if e.PersonID == person.ID {
				r.Why = e.Why
			}
		}
		if !slices.ContainsFunc(out, func(x Recipient) bool { return x.PersonID == r.PersonID }) {
			out = append(out, r)
		}
	}
	return out, nil
}

// match finds a person by email (bare or in angle brackets, case-insensitive) or by exact name.
func (d Directory) match(entry string) (Person, bool) {
	key := strings.ToLower(entry)
	if a, err := mail.ParseAddress(entry); err == nil {
		key = strings.ToLower(a.Address)
		for _, p := range d {
			if p.Email != "" && strings.ToLower(p.Email) == key {
				return p, true
			}
		}
		if a.Name != "" {
			key = strings.ToLower(a.Name)
		}
	}
	for _, p := range d {
		if strings.ToLower(p.Name) == key {
			return p, true
		}
	}
	return Person{}, false
}

type editInput struct{ to, cc, subject, body string }

// editFromView validates the Edit modal's required fields at the system boundary. Slack shows the
// returned field errors inline. Authoritative validation stays in core.
func editFromView(v *slack.View) (editInput, map[string]string) {
	errs := map[string]string{}
	in := editInput{to: inputValue(v, inputTo), cc: inputValue(v, inputCC), subject: inputValue(v, inputSubject), body: inputValue(v, inputBody)}
	if in.to == "" {
		errs[inputTo] = "At least one recipient is required."
	}
	if in.subject == "" {
		errs[inputSubject] = "A subject is required."
	}
	if in.body == "" {
		errs[inputBody] = "The email body cannot be empty."
	}
	return in, errs
}

func personIDs(rs []Recipient) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.PersonID)
	}
	return out
}

// commitFunc does the one core write of a modal submission and returns the follow-up that refreshes
// the Slack message. finish may be non-nil even when err is not, to show core's true state.
type commitFunc func(ctx context.Context) (finish func(context.Context) error, err error)

// viewSubmission handles a modal submit. Local validation answers Slack inline. The single core write
// then runs before the ack, bounded by ackBudget, so a failure shows inline in the still-open modal
// and the user keeps what they typed; the message refresh runs after the ack.
func (h *Handler) viewSubmission(ctx context.Context, cb slack.InteractionCallback) (any, Deferred) {
	t, err := DecodeTarget(cb.View.PrivateMetadata)
	if err != nil {
		h.log.Warn("ignoring view submission with a bad target", "callback", cb.View.CallbackID)
		return nil, nil
	}
	actor := actorOf(cb.User)
	field := func(block, msg string) any {
		return slack.NewErrorsViewSubmissionResponse(map[string]string{block: msg})
	}
	switch cb.View.CallbackID {
	case CallbackEditModal:
		in, errs := editFromView(&cb.View)
		if len(errs) > 0 {
			return slack.NewErrorsViewSubmissionResponse(errs), nil
		}
		return h.submitModal(ctx, cb, t, inputBody, func(ctx context.Context) (func(context.Context) error, error) {
			return h.applyEdit(ctx, t, actor, in)
		})
	case CallbackCorrectionModal:
		corrected := inputValue(&cb.View, inputCorrection)
		if corrected == "" {
			return field(inputCorrection, "Say what you meant."), nil
		}
		req := VerdictRequest{Verdict: VerdictCorrected, CorrectedStatement: corrected,
			Note: inputValue(&cb.View, inputNote), Surface: SurfaceSlack, ActorLabel: actor}
		return h.submitModal(ctx, cb, t, inputCorrection, h.interpretationCommit(t, req))
	case CallbackNoteModal:
		note := inputValue(&cb.View, inputNote)
		if note == "" {
			return field(inputNote, "Write a note first."), nil
		}
		req := VerdictRequest{Note: note, Surface: SurfaceSlack, ActorLabel: actor}
		return h.submitModal(ctx, cb, t, inputNote, h.verdictCommit(t, req))
	}
	return nil, nil
}

// interpretationCommit is Edit interpretation's write: the edit is stored as a correction plus its note. An
// interpretation saved unchanged is not a correction, so it is refused inline (Correct confirms it instead).
func (h *Handler) interpretationCommit(t Target, req VerdictRequest) commitFunc {
	commit := h.verdictCommit(t, req)
	return func(ctx context.Context) (func(context.Context) error, error) {
		inf, err := h.core.GetJudgmentInference(ctx, t.EpisodeID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.CorrectedStatement) == strings.TrimSpace(inf.InferredSemanticDelta.Statement) {
			return nil, &fieldError{inputCorrection, "That is still my interpretation. Edit it, or press Correct if it is right."}
		}
		return commit(ctx)
	}
}

func (h *Handler) verdictCommit(t Target, req VerdictRequest) commitFunc {
	return func(ctx context.Context) (func(context.Context) error, error) {
		inf, err := h.submitVerdict(ctx, t, req)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) error { return h.renderJudgment(ctx, t, inf) }, nil
	}
}

// submitModal runs commit under the retry guard and the episode lock.
func (h *Handler) submitModal(ctx context.Context, cb slack.InteractionCallback, t Target, errField string, commit commitFunc) (any, Deferred) {
	key := "view:" + cb.View.ID
	if !h.firstDelivery(key) {
		h.log.Info("duplicate delivery ignored", "callback", cb.View.CallbackID)
		return nil, nil
	}
	cctx, cancel := context.WithTimeout(ctx, ackBudget)
	defer cancel()
	var ack any
	unlock, locked := h.acquire(t.lockKey(), ackBudget/2)
	var finish func(context.Context) error
	var err error
	if locked {
		finish, err = commit(cctx)
		unlock()
	} else {
		err = errBusy
	}
	if err != nil {
		h.forget(key)
		h.log.ErrorContext(ctx, "modal submission failed", "callback", cb.View.CallbackID, "error", err)
		block, msg := modalError(err, errField)
		ack = slack.NewErrorsViewSubmissionResponse(map[string]string{block: msg})
	}
	if finish == nil {
		return ack, nil
	}
	return ack, func(ctx context.Context) {
		release, ok := h.acquire(t.lockKey(), workTimeout)
		if !ok {
			h.fail(ctx, t.ChannelID, cb.User.ID, cb.View.CallbackID, errBusy)
			return
		}
		defer release()
		if ferr := finish(ctx); ferr != nil {
			h.fail(ctx, t.ChannelID, cb.User.ID, cb.View.CallbackID, ferr)
		}
	}
}

// modalError is the inline text for a failed submission; it never carries internal detail.
func modalError(err error, defaultBlock string) (block, msg string) {
	var fe *fieldError
	var amb *AmbiguousError
	switch {
	case errors.Is(err, errBusy):
		return defaultBlock, "Another action on this run is still in progress. Wait a moment and press Submit again."
	case errors.As(err, &amb):
		return defaultBlock, "Ghost may not have saved this. Your text is still here; check the message, then press Submit again."
	case errors.As(err, &fe):
		return fe.block, fe.msg
	case errors.Is(err, errEditAfterSend):
		return defaultBlock, "This email was already sent, so your edit was not applied."
	}
	return defaultBlock, "Ghost could not save this right now. Your text is still here; press Submit again."
}

// applyEdit resolves the typed recipients to people and submits the edit to core. A no-op edit (same
// people, subject and body as what the human currently sees) is not written, so it cannot be
// recorded as a correction.
func (h *Handler) applyEdit(ctx context.Context, t Target, actor string, in editInput) (func(context.Context) error, error) {
	rs, dir, err := h.load(ctx, t.RunID)
	if err != nil {
		return nil, err
	}
	cur, err := h.core.GetStrategyDecision(ctx, t.RunID)
	if err != nil {
		return nil, err
	}
	cand, ok := rs.Candidate(cur.SelectedCandidateID)
	if !ok {
		return nil, ErrNotFound
	}
	render := func(dec HumanStrategyDecision) func(context.Context) error {
		return func(ctx context.Context) error {
			return h.renderSelected(ctx, t.RunID, dec, t.ChannelID, t.MessageTS, "")
		}
	}
	curTo, curCC, curArt := cand.To, cand.CC, cand.FullActionArtifact
	if cur.FinalTo != nil {
		curTo = cur.FinalTo
	}
	if cur.FinalCC != nil {
		curCC = cur.FinalCC
	}
	if cur.FinalArtifact != nil {
		curArt = *cur.FinalArtifact
	}
	to, err := parseRecipients(in.to, "to", inputTo, dir, curTo)
	if err != nil {
		return nil, err
	}
	if len(to) == 0 {
		return nil, &fieldError{inputTo, "At least one recipient is required."}
	}
	cc, err := parseRecipients(in.cc, "cc", inputCC, dir, curCC)
	if err != nil {
		return nil, err
	}
	if slices.Equal(personIDs(to), personIDs(curTo)) && slices.Equal(personIDs(cc), personIDs(curCC)) &&
		in.subject == curArt.SubjectText() && strings.TrimSpace(in.body) == strings.TrimSpace(curArt.Body) {
		return render(cur), nil
	}
	art := curArt
	subject := in.subject
	art.Subject, art.Body = &subject, in.body
	dec, err := h.core.RecordStrategyDecision(ctx, t.RunID, StrategyDecisionRequest{
		SelectedCandidateID: cand.CandidateID, Surface: SurfaceSlack, ActorLabel: actor,
		FinalTo: &to, FinalCC: &cc, FinalArtifact: &art,
	})
	if errors.Is(err, ErrConflict) { // already_decided: show the truth, do not pretend the edit applied
		truth, _, eerr := h.existing(ctx, t.RunID, err)
		if eerr != nil {
			return nil, eerr
		}
		return render(truth), errEditAfterSend
	}
	if err != nil {
		return nil, err
	}
	return render(dec), nil
}
