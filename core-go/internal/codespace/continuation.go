package codespace

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// The learning continuation (HAR-129 section F): what an earlier case learns reaches a later case and the later episode
// retrieves and USES it. Each step below is asserted against the case databases, so a recording that does not show the
// loop fails instead of freezing a demo whose last step would be untrue.

func idsOf(ks []FormedKnowledge) []string {
	out := make([]string, 0, len(ks))
	for _, k := range ks {
		out = append(out, k.ID)
	}
	return out
}

// awaitLearned waits for the human's correction to become knowledge (an entry that was not there before the human path),
// checks each is stamped at or before this case's Event N in replay time, and returns them.
func (r Recorder) awaitLearned(ctx context.Context, c Case, before []FormedKnowledge, eventN time.Time) ([]FormedKnowledge, error) {
	var fresh []FormedKnowledge
	err := r.waitUntil(ctx, "no knowledge formed from the human's correction in "+c.Label, func() (bool, error) {
		formed, err := r.Probe.Formed(ctx, c)
		if err != nil {
			return false, err
		}
		fresh = nil
		for _, k := range formed {
			if !slices.Contains(idsOf(before), k.ID) {
				fresh = append(fresh, k)
			}
		}
		return len(fresh) > 0, nil
	})
	if err != nil {
		return nil, err
	}
	for _, k := range fresh {
		if k.CreatedAt.After(eventN) {
			return nil, fmt.Errorf("knowledge %s was stamped %s, after the episode's Event N (%s) in replay time: a later episode could not see it (HAR-144)",
				k.ID, k.CreatedAt.UTC().Format(time.RFC3339), eventN.UTC().Format(time.RFC3339))
		}
	}
	r.Ops.say("%s: %d knowledge entries formed, stamped at or before Event N (%s)", c.Label, len(fresh), eventN.UTC().Format(time.RFC3339))
	return fresh, nil
}

// checkReceived proves the carry landed before the later episode is played: every entry the earlier cases formed is in
// this case's database with its OWN replay time (not the time of the latest entry, and not the wall clock).
func (r Recorder) checkReceived(ctx context.Context, c Case, learned []FormedKnowledge) error {
	if len(learned) == 0 {
		return nil
	}
	formed, err := r.Probe.Formed(ctx, c)
	if err != nil {
		return err
	}
	have := map[string]time.Time{}
	for _, k := range formed {
		have[k.ID] = k.CreatedAt
	}
	for _, want := range learned {
		at, ok := have[want.ID]
		switch {
		case !ok:
			return fmt.Errorf("knowledge %s learned earlier did not reach %s through the carry", want.ID, c.Label)
		case !at.Equal(want.CreatedAt):
			return fmt.Errorf("knowledge %s reached %s stamped %s, not its own replay time %s", want.ID, c.Label,
				at.UTC().Format(time.RFC3339), want.CreatedAt.UTC().Format(time.RFC3339))
		}
	}
	return nil
}

// checkApplied proves the later episode saw and used what was carried: each carried entry is stamped before this case's
// Event N, the run retrieved at least one, and its candidates cite (use) at least one. It returns the replay time of Event N.
func (r Recorder) checkApplied(ctx context.Context, c Case, played demorun.DemoState, learned []FormedKnowledge) (time.Time, error) {
	eventN, err := r.Probe.ReplayClock(ctx, c, played.RunID)
	if err != nil {
		return time.Time{}, err
	}
	if len(learned) == 0 {
		return eventN, nil
	}
	for _, k := range learned {
		if !k.CreatedAt.Before(eventN) {
			return time.Time{}, fmt.Errorf("carried knowledge %s is stamped %s, not before %s's Event N (%s): its as-of read could not see it",
				k.ID, k.CreatedAt.UTC().Format(time.RFC3339), c.Label, eventN.UTC().Format(time.RFC3339))
		}
	}
	retrieved, err := r.Probe.Retrieved(ctx, c, played.RunID)
	if err != nil {
		return time.Time{}, err
	}
	want := idsOf(learned)
	if !overlaps(retrieved, want) {
		return time.Time{}, fmt.Errorf("the %s episode retrieved none of the %d entries learned earlier (retrieved: %v)", c.Label, len(learned), retrieved)
	}
	used, err := r.Probe.Used(ctx, c, played.RunID)
	if err != nil {
		return time.Time{}, err
	}
	if !overlaps(used, want) {
		return time.Time{}, fmt.Errorf("the %s episode retrieved knowledge learned earlier but did not use it: none of its candidates cites one of the %d carried entries (used: %v)",
			c.Label, len(learned), used)
	}
	r.Ops.say("%s: the episode retrieved and used knowledge learned earlier", c.Label)
	return eventN, nil
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
