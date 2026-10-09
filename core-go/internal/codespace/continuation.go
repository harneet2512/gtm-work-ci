package codespace

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
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
	have, notOffered := map[string]time.Time{}, map[string]string{}
	for _, k := range formed {
		have[k.ID], notOffered[k.ID] = k.CreatedAt, k.NotOffered
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
		if why := notOffered[want.ID]; why != "" {
			return fmt.Errorf("knowledge %s reached %s but would not be offered to its episode: %s (the later case is not played: that is about 135 model calls for nothing)",
				want.ID, c.Label, why)
		}
	}
	return nil
}

// checkApplied proves the later episode was shown how close the carried lessons are (ADR-0013 amendment 2, closest match):
// each carried entry is stamped before this case's Event N, the run persisted its closest-match record (the closest lesson
// and its score) and scored the carried lessons, and a lesson at or above the threshold was offered to the agent. Whether
// the agent USED it is reported, never asserted: a lesson that is retrieved and offered is not thereby used or influential.
// It returns the replay time of Event N.
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
	return eventN, r.reportClosest(ctx, c, played, learned)
}

// reportClosest asserts the closest lesson and its score were surfaced and persisted, and that an above-threshold lesson
// was offered; it reports (does not assert) whether the offered lesson was used.
func (r Recorder) reportClosest(ctx context.Context, c Case, played demorun.DemoState, learned []FormedKnowledge) error {
	rv, err := r.Probe.Retrieval(ctx, c, played.RunID)
	if err != nil {
		return fmt.Errorf("the closest lesson and its score were not persisted for the %s episode: %w", c.Label, err)
	}
	var scored []string
	for _, cand := range rv.Candidates {
		scored = append(scored, cand.KnowledgeID)
	}
	want := idsOf(learned)
	if !overlaps(scored, want) {
		return fmt.Errorf("the %s episode scored none of the %d entries learned earlier (scored: %v): none reached its closest-match retrieval", c.Label, len(learned), scored)
	}
	r.Ops.say("%s: %s", c.Label, rv.Message)
	applicable, err := r.Probe.Applicable(ctx, c, played.RunID)
	if err != nil {
		return err
	}
	var offered []string
	for _, cand := range rv.Candidates {
		isOffered := slices.Contains(applicable, cand.KnowledgeID)
		if cand.Decision == knowledge.DecisionApplicable && !isOffered {
			return fmt.Errorf("lesson %s scored %.2f, at or above %.2f, but was not offered to the %s agent", cand.KnowledgeID, cand.Score, rv.Threshold, c.Label)
		}
		if isOffered && slices.Contains(want, cand.KnowledgeID) {
			offered = append(offered, cand.KnowledgeID)
		}
	}
	if len(offered) == 0 {
		return r.noContinuation(c, rv)
	}
	used, err := r.Probe.Used(ctx, c, played.RunID)
	if err != nil {
		return err
	}
	if overlaps(used, offered) {
		r.Ops.say("%s: offered lesson(s) %v were used: a candidate cites one (a citation, not a measure of influence)", c.Label, offered)
	} else {
		r.Ops.say("%s: offered lesson(s) %v were NOT used: no candidate cites one (reported, not asserted)", c.Label, offered)
	}
	return nil
}

// AllowNoContinuationEnv is the owner's opt-out: with it set to exactly 1 a record whose later case offers no lesson is accepted.
const AllowNoContinuationEnv = "GHOST_DEMO_ALLOW_NO_CONTINUATION"

// AllowNoContinuationFromEnv reads the opt-out; anything but "1" leaves it off.
func AllowNoContinuationFromEnv(getenv func(string) string) bool {
	return getenv(AllowNoContinuationEnv) == "1"
}

// noContinuation is the outcome when no carried lesson reached the threshold in the later case. The product's statement
// ("no similar knowledge") is honest, but HAR-129 step 32 asks for a later episode where the learned knowledge is
// applicable, so the record does not certify the continuation: it fails with the closest lesson, its score, the matched and
// differing features and the reason, unless the owner opted out (which is logged).
func (r Recorder) noContinuation(c Case, rv knowledge.Retrieval) error {
	detail := fmt.Sprintf("continuation not demonstrated in %s: %s (threshold %.2f)", c.Label, closestDetail(rv), rv.Threshold)
	if r.AllowNoContinuation {
		r.Ops.say("%s; accepted because %s=1: no carried lesson was offered, so none was offered and none could be used", detail, AllowNoContinuationEnv)
		return nil
	}
	return fmt.Errorf("%s; the learned knowledge did not become applicable in the later episode (HAR-129 step 32); set %s=1 only if the owner accepts a record without it", detail, AllowNoContinuationEnv)
}

func closestDetail(rv knowledge.Retrieval) string {
	if len(rv.Candidates) == 0 {
		return "no lesson was scored"
	}
	c := rv.Candidates[0]
	reason := "below the threshold"
	if c.Decision == knowledge.DecisionInsufficientFeatures {
		reason = fmt.Sprintf("too little to compare (%d comparable features, weight %g; needs %d and weight %g)", c.Comparable, c.ComparableWeight, rv.MinComparable, rv.MinWeight)
	}
	out := fmt.Sprintf("closest lesson %s (%s), similarity %.2f, matched on: %s, differs on: %s, reason: %s",
		c.Title, c.Status, c.Score, joinOrNothing(c.MatchedText()), joinOrNothing(c.DifferText()), reason)
	if len(c.Ignored) > 0 {
		out += ", not scored: " + strings.Join(c.Ignored, "; ")
	}
	return out
}

func joinOrNothing(in []string) string {
	if len(in) == 0 {
		return "nothing"
	}
	return strings.Join(in, "; ")
}

// reportCloseness prints, before the later case is played, how close the carried lessons are to it on the state it has
// before Event N, so what playing will show is known first. It never blocks: Event N's own claims are not ingested yet.
func (r Recorder) reportCloseness(ctx context.Context, c Case, learned []FormedKnowledge) {
	if len(learned) == 0 {
		return
	}
	rv, err := r.Probe.Closeness(ctx, c, learned)
	if err != nil {
		r.Ops.say("%s: could not estimate the similarity of the carried lessons before playing: %v", c.Label, err)
		return
	}
	r.Ops.say("%s: pre-check on the state before Event N (an estimate, Event N's own claims are not ingested yet; threshold %.2f): %s", c.Label, rv.Threshold, rv.Message)
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
