package slacksurface

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// MessageRef is where a posted message lives.
type MessageRef struct{ Channel, TS string }

// Publisher posts the three channel messages when told to (by core's outbox or the post command). The demo
// allows at most three channel messages per episode, so each is posted exactly once: when the Core is also a
// RefStore (the real core is), the guard is core's create-only message refs and survives any restart (refs.go).
// Otherwise (the fixture core of --dry-run) it is in-process only: a repeated call in one process returns the
// first message instead of posting a second.
type Publisher struct {
	core    Core
	poster  Poster
	refs    RefStore // nil: in-process guard only
	channel string
	webURL  string
	log     *slog.Logger
	mu      sync.Mutex
	posted  map[string]MessageRef
}

// NewPublisher builds a Publisher posting to channel. webURL is the web UI base for View account map.
func NewPublisher(core Core, poster Poster, channel, webURL string) *Publisher {
	p := &Publisher{core: core, poster: poster, channel: channel, webURL: webURL, log: slog.Default(), posted: map[string]MessageRef{}}
	if refs, ok := core.(RefStore); ok {
		p.refs = refs
	}
	return p
}

// prepared is a message ready to post and what its refs are keyed by.
type prepared struct {
	subject string
	msg     Message
	// mayRefresh says whether an existing message may be re-rendered over. nil: always. The chooser must not be
	// rendered back to "choose one" after the human chose.
	mayRefresh func(ctx context.Context) (bool, error)
}

// deliver posts the message once. key is the in-process guard of the no-refs mode.
func (p *Publisher) deliver(ctx context.Context, key, kind string, prepare func() (prepared, error)) (MessageRef, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refs == nil {
		if ref, ok := p.posted[key]; ok {
			return ref, nil
		}
	}
	pr, err := prepare()
	if err != nil {
		return MessageRef{}, err
	}
	pr.msg.Meta = &MessageMeta{SubjectID: pr.subject, Kind: kind}
	if p.refs != nil {
		return p.deliverRef(ctx, kind, pr)
	}
	ts, err := p.poster.PostMessage(ctx, p.channel, pr.msg)
	if err != nil {
		return MessageRef{}, err
	}
	ref := MessageRef{Channel: p.channel, TS: ts}
	p.posted[key] = ref
	return ref, nil
}

// PostBI posts Message 1 for the account's latest business-intelligence update.
func (p *Publisher) PostBI(ctx context.Context, accountID string) (MessageRef, error) {
	return p.PostBIUpdate(ctx, accountID, "")
}

// PostBIUpdate posts Message 1 for the named update, which must still be the account's latest (updateID empty:
// whichever is). Otherwise ErrSuperseded: only the latest update is rendered.
func (p *Publisher) PostBIUpdate(ctx context.Context, accountID, updateID string) (MessageRef, error) {
	return p.deliver(ctx, "bi:"+accountID, KindBI, func() (prepared, error) {
		u, err := p.core.GetBIUpdate(ctx, accountID)
		if err != nil {
			return prepared{}, err
		}
		if updateID != "" && u.ID != updateID {
			return prepared{}, fmt.Errorf("%w: %s is not the latest (%s)", ErrSuperseded, updateID, u.ID)
		}
		name, err := p.core.GetAccountName(ctx, accountID)
		if err != nil {
			return prepared{}, err
		}
		return prepared{subject: u.ID, msg: RenderBI(u, name, MapURL(p.webURL, u))}, nil
	})
}

// PostChooser posts Message 2. Core must already hold all three complete, evaluated candidates.
func (p *Publisher) PostChooser(ctx context.Context, runID string) (MessageRef, error) {
	return p.deliver(ctx, "chooser:"+runID, KindChooser, func() (prepared, error) {
		rs, err := p.core.GetStrategies(ctx, runID)
		if err != nil {
			return prepared{}, err
		}
		if len(rs.StrategySet.Candidates) != 3 {
			return prepared{}, fmt.Errorf("slacksurface: strategy set has %d candidates, the demo path needs exactly 3", len(rs.StrategySet.Candidates))
		}
		dir, err := p.core.People(ctx, rs.StrategySet.AccountID)
		if err != nil {
			return prepared{}, err
		}
		return prepared{subject: rs.StrategySet.DecisionEpisodeID, msg: RenderChooser(rs, dir), mayRefresh: p.stillUndecided(runID)}, nil
	})
}

// stillUndecided reports whether the human has not chosen yet, which is the only time Message 2 may be re-rendered.
func (p *Publisher) stillUndecided(runID string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		_, err := p.core.GetStrategyDecision(ctx, runID)
		switch {
		case err == nil:
			return false, nil
		case errors.Is(err, ErrNotFound):
			return true, nil
		default:
			return false, err
		}
	}
}

// PostJudgment posts Message 3 once core has the inference (ErrNotReady until then).
func (p *Publisher) PostJudgment(ctx context.Context, runID, episodeID string) (MessageRef, error) {
	return p.deliver(ctx, "judgment:"+episodeID, KindJudgment, func() (prepared, error) {
		inf, err := p.core.GetJudgmentInference(ctx, episodeID)
		if err != nil {
			return prepared{}, err
		}
		rs, err := p.core.GetStrategies(ctx, runID)
		if err != nil {
			return prepared{}, err
		}
		return prepared{subject: episodeID, msg: RenderJudgment(inf, rs, runID)}, nil
	})
}
