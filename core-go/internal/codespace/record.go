package codespace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// Recorder is the one-time record run: one chronological replay across the cases, the way the demo plays it. Case 1 is
// played and the human works through Cliff's Slack handlers (script.go, slackhuman.go); the correction forms knowledge;
// then the same hidden handoff the audience's second Play triggers (Ops.Handoff) carries it into the later case, whose
// episode must retrieve AND use it (continuation.go). Everything is reset to the start at the end. Every model call it
// causes goes through the cache (GHOST_LLM_MODE=cache) and is stored once; running it again under a strict cache must
// pass without a single miss, which proves a rehearsal costs nothing.
type Recorder struct {
	Ops    Ops
	Player Player
	Human  Human
	// Probe reads what learning did from the case databases; the continuation checks need it.
	Probe  KnowledgeProbe
	Script Script
	// Gates, CacheCalls and Misses make the run wait for the background gates and fail on a strict miss; nil skips each.
	Gates      GateProbe
	CacheCalls func() (int, error)
	Misses     func() (int, error)
	Poll       time.Duration
	Wait       time.Duration
	Sleep      func(ctx context.Context, d time.Duration) error
	// Sheet receives what the human typed, to write the presenter's run sheet; nil skips it.
	Sheet func(RunSheet) error
}

// Run records every case in order, then resets the whole chronology to Event N-1 of case 1. It starts from a reset
// chronology too, so a run that stopped half way can simply be run again: the calls already stored are replayed, not
// repeated. When the run fails the chronology is put back as well (best effort), so a failed record never leaves the
// demo in a played state.
func (r Recorder) Run(ctx context.Context) (err error) {
	if r.Probe == nil {
		return errors.New("the record run needs a knowledge probe to prove the learning continuation")
	}
	if r.Human == nil {
		return errors.New("the record run needs a human to play the Slack path")
	}
	r.Ops.say("starting from a reset chronology")
	if err := r.Ops.ResetAll(ctx, nil); err != nil {
		return fmt.Errorf("reset before recording: %w", err)
	}
	defer func() {
		if err != nil {
			if rerr := r.Ops.ResetAll(context.WithoutCancel(ctx), nil); rerr != nil {
				err = errors.Join(err, fmt.Errorf("reset after the failure: %w", rerr))
			}
		}
	}()
	startMisses := 0
	if r.Misses != nil {
		if startMisses, err = r.Misses(); err != nil {
			return fmt.Errorf("read the cache misses: %w", err)
		}
	}
	var sheet RunSheet
	var learned []FormedKnowledge // knowledge formed by the cases already recorded
	var prev demorun.DemoState
	for i, c := range r.Ops.Cases {
		if err := r.enter(ctx, i, c, prev); err != nil {
			return fmt.Errorf("record %s: %w", c.Label, err)
		}
		path, formed, st, rerr := r.recordCase(ctx, c, learned, i == len(r.Ops.Cases)-1)
		if rerr != nil {
			return fmt.Errorf("record %s: %w", c.Label, rerr)
		}
		sheet.Paths = append(sheet.Paths, path)
		learned = append(learned, formed...)
		prev = st
	}
	if r.Sheet != nil {
		if err := r.Sheet(sheet); err != nil {
			return fmt.Errorf("write the run sheet: %w", err)
		}
	}
	if err := r.checkNoMisses(startMisses); err != nil {
		return err
	}
	r.Ops.say("resetting the whole chronology to Event N-1")
	return r.Ops.ResetAll(ctx, nil)
}

// enter makes the case the one core serves. Case 1 already is after the reset; every later case is entered through the
// same hidden handoff the audience's second Play uses.
func (r Recorder) enter(ctx context.Context, i int, c Case, prev demorun.DemoState) error {
	if i == 0 {
		if active, _ := ReadMarker(r.Ops.Paths.ActiveFile()); active == c.Slot {
			return nil
		}
		_, err := r.Ops.Activate(ctx, c.Slot)
		return err
	}
	r.Ops.say("continuing the chronology into %s (the handoff behind the next Play)", c.Label)
	next, err := r.Ops.Handoff(ctx, prev.ManifestID)
	if err != nil {
		return err
	}
	if next.Slot != c.Slot {
		return fmt.Errorf("the handoff went to %s, want %s", next.Slot, c.Slot)
	}
	return nil
}

// recordCase plays one case and its human path. learned are the entries the earlier cases formed: the case must have
// received them with their own replay times, and its episode must retrieve and use them. For a case that is not the last it
// returns the entries this case formed from the human's correction. It also returns the played state.
func (r Recorder) recordCase(ctx context.Context, c Case, learned []FormedKnowledge, last bool) (HumanPath, []FormedKnowledge, demorun.DemoState, error) {
	st, ok, err := r.Ops.SeedState(c.Slot)
	if err != nil || !ok {
		return HumanPath{}, nil, st, fmt.Errorf("%s is not seeded: %v", c.Slot, errOr(err, "no seed state"))
	}
	if err := r.checkReceived(ctx, c, learned); err != nil {
		return HumanPath{}, nil, st, err
	}
	r.Ops.say("%s: playing Event N through the real pipeline (BI update, strategies, evals)", c.Label)
	played, err := r.Player.Play(ctx, st)
	if err != nil {
		return HumanPath{}, nil, st, err
	}
	if played.RunID == "" || played.EpisodeID == "" {
		return HumanPath{}, nil, st, errors.New("Play finished without a published run and decision episode")
	}
	eventN, err := r.checkApplied(ctx, c, played, learned)
	if err != nil {
		return HumanPath{}, nil, played, err
	}
	var before []FormedKnowledge
	if !last {
		if before, err = r.Probe.Formed(ctx, c); err != nil {
			return HumanPath{}, nil, played, err
		}
	}
	path, err := r.Human.Act(ctx, c, played, r.Script)
	if err != nil {
		return path, nil, played, err
	}
	r.Ops.say("%s: waiting for every gate row to be persisted", c.Label)
	if err := r.awaitGates(ctx, c, played.EpisodeID); err != nil {
		return path, nil, played, err
	}
	if last {
		return path, nil, played, nil
	}
	formed, err := r.awaitLearned(ctx, c, before, eventN)
	return path, formed, played, err
}

// waitUntil polls ready every Poll until it says yes, fails, or Wait has passed.
func (r Recorder) waitUntil(ctx context.Context, timeoutMsg string, ready func() (bool, error)) error {
	poll, wait := r.Poll, r.Wait
	if poll <= 0 {
		poll = 3 * time.Second
	}
	if wait <= 0 {
		wait = 15 * time.Minute
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		}
	}
	var waited time.Duration
	for {
		ok, err := ready()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if waited >= wait {
			return fmt.Errorf("%s within %s", timeoutMsg, wait)
		}
		if err := sleep(ctx, poll); err != nil {
			return err
		}
		waited += poll
	}
}
