package slacksurface

import (
	"context"
	"errors"
	"fmt"
)

// deliverRef is the exactly-once flow of refs.go for one message. The caller holds p.mu.
func (p *Publisher) deliverRef(ctx context.Context, kind string, pr prepared) (MessageRef, error) {
	meta := *pr.msg.Meta

	ref, err := p.refs.GetRef(ctx, pr.subject, kind)
	created := false
	switch {
	case err == nil:
	case errors.Is(err, ErrNotFound):
		// Reserve before posting: from here on a crash leaves evidence that a post may have happened.
		if ref, created, err = p.refs.ReserveRef(ctx, pr.subject, kind, p.channel); err != nil {
			return MessageRef{}, fmt.Errorf("slacksurface: reserve the %s message of %s: %w", kind, pr.subject, err)
		}
	default:
		return MessageRef{}, fmt.Errorf("slacksurface: read the %s message ref of %s: %w", kind, pr.subject, err)
	}

	if ref.TS != "" { // it exists: refresh it, never post again
		p.refresh(ctx, ref, pr)
		return MessageRef{Channel: ref.Channel, TS: ref.TS}, nil
	}
	if !created { // a reservation without a ts: an earlier attempt may have posted
		ts, found, err := p.poster.FindMessage(ctx, ref.Channel, ref.ReservedAt.Add(-refLookback), meta)
		if err != nil {
			return MessageRef{}, fmt.Errorf("slacksurface: reconcile the %s message of %s: %w", kind, pr.subject, err)
		}
		if found {
			p.log.Info("adopted a message posted by an earlier attempt", "kind", kind, "subject", pr.subject, "ts", ts)
			return p.record(ctx, ref, ts)
		}
	}
	ts, err := p.poster.PostMessage(ctx, ref.Channel, pr.msg)
	if err != nil {
		return MessageRef{}, err // the reservation stays; the next delivery reconciles and posts
	}
	return p.record(ctx, ref, ts)
}

// record stores the ts of a message that is now visible (posted or adopted). If another process recorded a
// different ts first, the message in hand is the duplicate: it is deleted and the recorded one is returned.
func (p *Publisher) record(ctx context.Context, ref RefRecord, ts string) (MessageRef, error) {
	_, err := p.refs.RecordRefTS(ctx, ref.SubjectID, ref.Kind, ts)
	var conflict *RefTSConflictError
	switch {
	case err == nil:
		return MessageRef{Channel: ref.Channel, TS: ts}, nil
	case errors.As(err, &conflict):
		if derr := p.poster.DeleteMessage(ctx, ref.Channel, ts); derr != nil {
			p.log.Error("could not delete a duplicate message", "kind", ref.Kind, "subject", ref.SubjectID, "ts", ts, "error", derr)
		}
		return MessageRef{Channel: conflict.Existing.Channel, TS: conflict.Existing.TS}, nil
	default:
		// The message is visible but its ts is not stored: the redelivery finds it in the history and adopts it.
		return MessageRef{}, fmt.Errorf("slacksurface: store the ts of the %s message of %s: %w", ref.Kind, ref.SubjectID, err)
	}
}

// refresh re-renders an existing message in place. It is best effort: the message exists, so a failed refresh is
// logged and does not hold the event back.
func (p *Publisher) refresh(ctx context.Context, ref RefRecord, pr prepared) {
	if pr.mayRefresh != nil {
		ok, err := pr.mayRefresh(ctx)
		if err != nil {
			p.log.Warn("could not tell whether the message may be refreshed; left as it is", "kind", ref.Kind, "subject", ref.SubjectID, "error", err)
			return
		}
		if !ok {
			return
		}
	}
	if err := p.poster.UpdateMessage(ctx, ref.Channel, ref.TS, pr.msg); err != nil {
		p.log.Warn("could not refresh an existing message", "kind", ref.Kind, "subject", ref.SubjectID, "ts", ref.TS, "error", err)
	}
}
