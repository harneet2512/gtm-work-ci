package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
)

// Core serves the Play endpoints, and without Neo4j or a dataset says so before it releases anything.
func TestCoreServesPlayAndSaysWhatIsMissing(t *testing.T) {
	t.Setenv("NEO4J_URI", "")
	world := replaytest.SeedWorld(t, env.DB)
	held := replaytest.NewHeldOut(2)
	manifest := replaytest.InsertManifest(t, env.DB, world, held)

	base, stop := startCore(t, testConfig("127.0.0.1:0"))
	defer func() {
		if err := stop(); err != nil {
			t.Errorf("stop: %v", err)
		}
	}()

	code := func(body string) string {
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal([]byte(body), &e)
		return e.Error.Code
	}

	if c, body := request(t, http.MethodPost, base+"/replay/play", "", `{"manifest_id":"`+manifest+`"}`); c != http.StatusUnauthorized {
		t.Fatalf("unauthenticated Play = %d %s", c, body)
	}
	if c, body := request(t, http.MethodPost, base+"/replay/play", apiToken, `{"manifest_id":"99999999-9999-4999-8999-999999999999"}`); c != http.StatusNotFound || code(body) != "manifest_not_found" {
		t.Fatalf("unknown manifest = %d %s: the route must be wired", c, body)
	}
	if c, body := request(t, http.MethodPost, base+"/replay/play", apiToken, `{"manifest_id":"`+manifest+`"}`); c != http.StatusServiceUnavailable || code(body) != "graph_unavailable" {
		t.Fatalf("Play without Neo4j = %d %s", c, body)
	}
	if c, body := request(t, http.MethodGet, base+"/replay/manifests/"+manifest+"/invisibility", apiToken, ""); c != http.StatusServiceUnavailable || code(body) != "graph_unavailable" {
		t.Fatalf("invisibility without Neo4j = %d %s: an unchecked graph is never clean", c, body)
	}
	if n := replaytest.One(t, env.DB, `SELECT count(*)::text FROM source_events`); n != "0" {
		t.Fatalf("a refused Play released something: source_events = %s", n)
	}
}

func TestNewReplayReadsTheDatasetPathAndStaysUpWithoutIt(t *testing.T) {
	logger, logs := quietLogger()
	cfg := testConfig("127.0.0.1:0")
	if _, err := newReplay(env.DB, nil, cfg, logger, nil); err != nil {
		t.Fatalf("no dataset, no graph must still build: %v", err)
	}
	for _, want := range []string{"GHOST_REPLAY_EVENTS is empty", "NEO4J_URI is empty"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("startup log lacks %q:\n%s", want, logs)
		}
	}
	cfg.ReplayEventsPath = filepath.Join(t.TempDir(), "events.json")
	if err := os.WriteFile(cfg.ReplayEventsPath, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newReplay(env.DB, nil, cfg, logger, nil); err != nil {
		t.Fatalf("with a dataset path: %v", err)
	}
}
