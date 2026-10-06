package replaycontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/play"
)

// episodeRules loads the lifecycle rules the episode view's knowledge-as-of read replays, found by walking
// up from the working directory like openapitest does for core.yaml.
func episodeRules(t *testing.T) *knowledge.Rules {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		cand := filepath.Join(dir, "contracts", "knowledge", "lifecycle.v1.json")
		if _, err := os.Stat(cand); err == nil {
			rules, err := knowledge.LoadRules(cand)
			if err != nil {
				t.Fatal(err)
			}
			return &rules
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("contracts/knowledge/lifecycle.v1.json not found above the working directory")
		}
		dir = filepath.Dir(dir)
	}
}

func fields(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	return m
}

const (
	episodesPath = "/replay/manifests/{manifest_id}/episodes"
	nextPath     = "/replay/manifests/{manifest_id}/episodes/next"
	resetPath    = "/replay/manifests/{manifest_id}/reset"
)

func (s *server) episodes(id, query string) reply {
	return s.do("GET", "/replay/manifests/"+id+"/episodes"+query, episodesPath, apiToken, nil)
}

func (s *server) next(id string) reply {
	return s.do("POST", "/replay/manifests/"+id+"/episodes/next", nextPath, apiToken, nil)
}

func (s *server) reset(id string, body any) reply {
	raw, _ := json.Marshal(body)
	return s.do("POST", "/replay/manifests/"+id+"/reset", resetPath, apiToken, raw)
}

// TestTheEpisodeSurfaceOverHTTPConformsToTheContract walks the whole episode flow over HTTP: view before
// and after advances, Previous via ?at=, reset and its deterministic digest, the held-out episode through
// Play itself, and the error envelope of every documented failure.
func TestTheEpisodeSurfaceOverHTTPConformsToTheContract(t *testing.T) {
	s := newServer(t, func(o *play.Options) { o.Rules = episodeRules(t) })
	m := s.manifest

	// before any release: the view is episode 0 and event 1 is next, withheld
	r := s.episodes(m, "")
	if r.status != 200 {
		t.Fatalf("GET episodes: %d %s", r.status, r.body)
	}
	v := fields(t, r.body)
	if v["episode"] != 0.0 || v["total"] != 3.0 || v["state"] != nil ||
		v["next_event"].(map[string]any)["position"] != 1.0 {
		t.Fatalf("view before any release: %s", r.body)
	}

	// advance the two history episodes: adopted from the trail the frozen world left
	for k := 1; k <= 2; k++ {
		r = s.next(m)
		if r.status != 200 {
			t.Fatalf("next %d: %d %s", k, r.status, r.body)
		}
		a := fields(t, r.body)
		if a["episode"] != float64(k) {
			t.Fatalf("advanced to %v, want %d: %s", a["episode"], k, r.body)
		}
	}
	// the cursor view is episode 2; event 3 is next (the two history events folded into one version)
	r = s.episodes(m, "")
	v = fields(t, r.body)
	if v["episode"] != 2.0 || len(v["prior_episodes"].([]any)) != 2 ||
		v["state"].(map[string]any)["version"] != 1.0 ||
		v["next_event"].(map[string]any)["position"] != 3.0 || v["can_play_next"] != true {
		t.Fatalf("view at the cursor: %s", r.body)
	}
	// Previous: ?at=1 shows the world of episode 1 and event 2 next again — no state yet, because the
	// version the world has covers event 2's world time and is correctly beyond episode 1's bound
	r = s.episodes(m, "?at=1")
	v = fields(t, r.body)
	if v["episode"] != 1.0 || v["state"] != nil ||
		v["next_event"].(map[string]any)["position"] != 2.0 {
		t.Fatalf("view at=1: %s", r.body)
	}
	// an unreleased position is refused
	r = s.episodes(m, "?at=3")
	if r.status != 422 || code(t, r) != "invalid_episode" {
		t.Fatalf("at=3: %d %s", r.status, r.body)
	}
	r = s.episodes(m, "?at=-1")
	if r.status != 400 {
		t.Fatalf("at=-1: %d %s", r.status, r.body)
	}

	// reset to 1: deterministic — two resets answer the same released events and digest
	r = s.reset(m, map[string]any{"episode": 1})
	if r.status != 200 {
		t.Fatalf("reset: %d %s", r.status, r.body)
	}
	digest := fields(t, r.body)["digest"]
	again := s.reset(m, map[string]any{"episode": 1})
	if fields(t, again.body)["digest"] != digest {
		t.Fatalf("reset digests differ: %v != %v", digest, fields(t, again.body)["digest"])
	}
	// and the view is back at episode 1
	v = fields(t, s.episodes(m, "").body)
	if v["episode"] != 1.0 {
		t.Fatalf("view after reset: %s", v)
	}
	// a reset target that was never released is refused, and so is a malformed body
	r = s.reset(m, map[string]any{"episode": 3})
	if r.status != 422 || code(t, r) != "invalid_episode" {
		t.Fatalf("reset(3): %d %s", r.status, r.body)
	}
	r = s.reset(m, map[string]any{"episode": -1})
	if r.status != 400 {
		t.Fatalf("reset(-1): %d %s", r.status, r.body)
	}

	// the held-out episode goes through Play: re-advancing after the reset adopts episode 2's row,
	// then event 3 is released through the real pipeline
	s.next(m)
	r = s.next(m)
	if r.status != 200 {
		t.Fatalf("held-out advance: %d %s", r.status, r.body)
	}
	a := fields(t, r.body)
	if a["episode"] != 3.0 || a["material"] != true || a["account_change_id"] == nil {
		t.Fatalf("held-out advance: %s", r.body)
	}
	// the replay is complete
	r = s.next(m)
	if r.status != 409 || code(t, r) != "replay_complete" {
		t.Fatalf("advance past the end: %d %s", r.status, r.body)
	}
	// an unknown manifest is 404 on every episode route
	ghost := "33333333-3333-4333-8333-333333333333"
	if r := s.episodes(ghost, ""); r.status != 404 {
		t.Fatalf("episodes of a missing manifest: %d %s", r.status, r.body)
	}
	if r := s.next(ghost); r.status != 404 {
		t.Fatalf("next of a missing manifest: %d %s", r.status, r.body)
	}
	if r := s.reset(ghost, map[string]any{"episode": 0}); r.status != 404 {
		t.Fatalf("reset of a missing manifest: %d %s", r.status, r.body)
	}
}

// TestTheEpisodeViewFailsClosedWithoutKnowledgeRules covers the 503: without the lifecycle rules the view
// cannot age knowledge, so it refuses rather than answer with knowledge it cannot trust.
func TestTheEpisodeViewFailsClosedWithoutKnowledgeRules(t *testing.T) {
	s := newServer(t, nil)
	r := s.episodes(s.manifest, "")
	if r.status != 503 || code(t, r) != "knowledge_unavailable" {
		t.Fatalf("view without lifecycle rules: %d %s", r.status, r.body)
	}
}
