package play

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
)

func TestTheEpisodeViewIsTheWorldAtTheReleasedCursor(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 2)

	doc, err := r.svc.Episodes(bg, r.manifest, nil)
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	validate(t, "episode_replay", doc)
	v := resultField(t, doc)
	if v["episode"] != 2.0 || v["total"] != 3.0 || v["window"] != "historical" {
		t.Fatalf("view: %+v", v)
	}
	if v["can_previous"] != true || v["can_play_next"] != true {
		t.Fatalf("previous/next flags: %+v", v)
	}
	st := v["state"].(map[string]any)
	if st["version"] != 2.0 || st["digest"] == nil || st["document"] == nil {
		t.Fatalf("state at episode 2: %+v", st)
	}
	prior := v["prior_episodes"].([]any)
	if len(prior) != 2 {
		t.Fatalf("prior_episodes = %v", prior)
	}
	first := prior[0].(map[string]any)
	if first["position"] != 1.0 || first["released"] != true || first["material"] != true {
		t.Fatalf("prior episode 1: %+v", first)
	}
	second := prior[1].(map[string]any)
	if second["position"] != 2.0 || second["material"] != false || second["no_action_reason"] != "no_material_change" {
		t.Fatalf("prior episode 2: %+v", second)
	}
	next := v["next_event"].(map[string]any)
	if next["position"] != 3.0 || next["released"] != false || next["held_out"] != true ||
		next["material"] != nil || next["state_version"] != nil || next["account_change_id"] != nil {
		t.Fatalf("the withheld next event: %+v", next)
	}
	boundary := v["boundary"].(map[string]any)
	if boundary["historical_start"] != 1.0 || boundary["historical_end"] != 2.0 ||
		boundary["live_start"] != 3.0 || boundary["live_end"] != 3.0 {
		t.Fatalf("boundary: %+v", boundary)
	}
}

func TestTheViewAtAnEarlierEpisodeReadsItsOwnWorld(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 2)
	at := 1

	doc, err := r.svc.Episodes(bg, r.manifest, &at)
	if err != nil {
		t.Fatalf("Episodes(at=1): %v", err)
	}
	validate(t, "episode_replay", doc)
	v := resultField(t, doc)
	if v["episode"] != 1.0 || v["can_previous"] != true || v["can_play_next"] != true {
		t.Fatalf("view at 1: %+v", v)
	}
	st := v["state"].(map[string]any)
	if st["version"] != 1.0 {
		t.Fatalf("the state is the one episode 1 ended in, not the latest: %+v", st)
	}
	if len(v["prior_episodes"].([]any)) != 1 {
		t.Fatalf("prior episodes at 1: %v", v["prior_episodes"])
	}
	next := v["next_event"].(map[string]any)
	if next["position"] != 2.0 || next["released"] != false || next["material"] != nil {
		t.Fatalf("viewed from episode 1, event 2 is again unreleased and withheld: %+v", next)
	}
}

func TestTheViewBeforeAnyReleaseShowsOnlyWhatComesFirst(t *testing.T) {
	r := newEpisodeRig(t)

	doc, err := r.svc.Episodes(bg, r.manifest, nil)
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	validate(t, "episode_replay", doc)
	v := resultField(t, doc)
	if v["episode"] != 0.0 || v["window"] != "none" || v["state"] != nil ||
		len(v["prior_episodes"].([]any)) != 0 || v["can_previous"] != false || v["can_play_next"] != true {
		t.Fatalf("view before any release: %+v", v)
	}
	next := v["next_event"].(map[string]any)
	if next["position"] != 1.0 || next["held_out"] != false || next["released"] != false {
		t.Fatalf("next event at 0: %+v", next)
	}
}

func TestTheViewAfterTheLastEpisodeHasNothingNext(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 3)

	doc, err := r.svc.Episodes(bg, r.manifest, nil)
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	validate(t, "episode_replay", doc)
	v := resultField(t, doc)
	if v["episode"] != 3.0 || v["window"] != "live" || v["next_event"] != nil ||
		v["can_previous"] != true || v["can_play_next"] != false {
		t.Fatalf("view after the last episode: %+v", v)
	}
}

func TestTheViewRefusesAnEpisodeThatWasNeverReleased(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 1)
	for _, at := range []int{-1, 2, 3} {
		if _, err := r.svc.Episodes(bg, r.manifest, &at); !errors.Is(err, ErrInvalidEpisode) {
			t.Fatalf("Episodes(at=%d): %v", at, err)
		}
	}
}

func TestTheViewCannotReadKnowledgeWithoutItsRules(t *testing.T) {
	r := newEpisodeRig(t)
	svc := r.service(func(o *Options) { o.Rules = nil })
	if _, err := svc.Episodes(bg, r.manifest, nil); !errors.Is(err, ErrKnowledgeUnavailable) {
		t.Fatalf("view without lifecycle rules: %v", err)
	}
}

func TestKnowledgeIsReadAsOfTheEpisode(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 3)
	// K-old earned provisional before episode 1's bound; K-late only before episode 3's.
	old := seedKnowledge(t, "old knowledge", replaytest.T0.Add(-4*time.Hour), replaytest.T0.Add(-150*time.Minute))
	late := seedKnowledge(t, "late knowledge", replaytest.T0.Add(45*time.Minute), replaytest.T0.Add(46*time.Minute))

	knowledgeIDs := func(k int) map[string]bool {
		t.Helper()
		doc, err := r.svc.Episodes(bg, r.manifest, &k)
		if err != nil {
			t.Fatalf("Episodes(at=%d): %v", k, err)
		}
		items := resultField(t, doc)["knowledge"].(map[string]any)["items"].([]any)
		out := make(map[string]bool, len(items))
		for _, it := range items {
			m := it.(map[string]any)
			out[fmt.Sprint(m["id"])] = true
			if m["status"] != "provisional" {
				t.Fatalf("knowledge status at episode %d: %+v", k, m)
			}
		}
		return out
	}
	if at1 := knowledgeIDs(1); !at1[old] || at1[late] {
		t.Fatalf("knowledge as of episode 1: %v (old %s must apply, late %s must not)", at1, old, late)
	}
	if at3 := knowledgeIDs(3); !at3[old] || !at3[late] {
		t.Fatalf("knowledge as of episode 3: %v (both must apply)", at3)
	}
}

func TestTheHistoricalWindowIsConfiguredPerDeployment(t *testing.T) {
	r := newEpisodeRig(t)
	svc := r.service(func(o *Options) {
		o.Rules = episodeRules(t)
		h := 1
		o.HistoricalEnd = &h
	})
	advanceEpisodes(t, r, 2)

	doc, err := svc.Episodes(bg, r.manifest, nil)
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	validate(t, "episode_replay", doc)
	v := resultField(t, doc)
	boundary := v["boundary"].(map[string]any)
	if boundary["historical_end"] != 1.0 || boundary["live_start"] != 2.0 || v["window"] != "live" {
		t.Fatalf("a 1-event historical window: %+v", v)
	}
}
