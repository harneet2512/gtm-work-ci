package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

const soc2Body = "We need the SOC2 report before signing. Thanks"

func count(t *testing.T, q string) string { return replaytest.One(t, env.DB, q) }

// The demo path end to end on a running core with Neo4j: the held-out event lives only in the replay dataset,
// the invisibility check passes against the real graph, Play releases it through the real ingest, the real
// coalescer and the real projector, and the update it writes is served by the existing read endpoint.
func TestPlayThroughARunningCoreWithNeo4j(t *testing.T) {
	setGraphEnv(t)
	purge := `TRUNCATE demo_plays, demo_manifests, account_changes, business_intelligence_updates, graph_projection_jobs, relationships, state_history,
		account_state, recompute_jobs, claims, extraction_cache, unresolved_activities, activity_participants, activities, source_events,
		entity_source_mappings, opportunities, people, accounts RESTART IDENTITY CASCADE`
	if err := storetest.Purge(context.Background(), env.DB, purge); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storetest.Purge(context.Background(), env.DB, purge) })

	world := replaytest.World{}
	world.Account = replaytest.One(t, env.DB, `INSERT INTO accounts (name, domain) VALUES ('Wired Co', 'wired-test.example') RETURNING id::text`)
	world.Opportunity = replaytest.One(t, env.DB, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, 'Wired expansion', 'expansion') RETURNING id::text`, world.Account)

	from, to := "priya@wired-test.example", "dana@vendor.example"
	held := replaytest.HeldOut{EventID: "0e7e0000-0000-4000-8000-0000000000e2",
		Event: replaytest.EmailBetween(50, time.Date(2026, 10, 3, 9, 30, 0, 0, time.UTC), "inbound", from, to, soc2Body)}
	dataset := filepath.Join(t.TempDir(), "replay.json")
	raw, _ := json.Marshal([]any{held.Event})
	if err := os.WriteFile(dataset, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// the history the manifest describes is the two emails ingested below: event N-1 is the second
	history := make([]normalize.SourceEvent, 0, 2)
	for i := 1; i <= 2; i++ {
		history = append(history, replaytest.EmailBetween(i, time.Date(2026, 10, 3, 7+i, 0, 0, 0, time.UTC), "inbound", from, to, "Thanks for the update"))
	}
	manifest := replaytest.InsertManifest(t, env.DB, world, held, history...)

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "need the SOC2") {
			_, _ = io.WriteString(w, workerAnswer)
			return
		}
		_, _ = io.WriteString(w, `{"claims":[],"model":"stub-model","extractor_version":"extract-v1","dropped":0}`)
	}))
	defer worker.Close()
	cfg := testConfig("127.0.0.1:0")
	cfg.WorkerURL, cfg.ReplayEventsPath = worker.URL, dataset
	base, stop := startCore(t, cfg)
	defer func() { _ = stop() }()

	// history: two emails, recomputed and projected before the demo starts
	for _, ev := range history {
		body, _ := json.Marshal(ev)
		if code, resp := request(t, http.MethodPost, base+"/ingest", apiToken, string(body)); code != http.StatusCreated {
			t.Fatalf("history ingest = %d %s", code, resp)
		}
	}
	waitFor(t, "the history to be recomputed and projected", func() bool {
		return count(t, `SELECT count(*)::text FROM state_diffs`) != "0" &&
			count(t, `SELECT count(*)::text FROM graph_projection_jobs WHERE completed_at IS NULL`) == "0" &&
			count(t, `SELECT count(*)::text FROM recompute_jobs`) == "0"
	})

	// before Play: nothing derived from event N exists, in Postgres, AccountState or the real graph
	if code, body := request(t, http.MethodGet, base+"/replay/manifests/"+manifest+"/invisibility", apiToken, ""); code != http.StatusOK || !strings.Contains(body, `"status":"withheld"`) {
		t.Fatalf("invisibility before Play = %d %s", code, body)
	}

	code, body := request(t, http.MethodPost, base+"/replay/play", apiToken, `{"manifest_id":"`+manifest+`"}`)
	if code != http.StatusOK {
		t.Fatalf("Play = %d %s", code, body)
	}
	var result struct {
		ReplayWorld   json.RawMessage `json:"replay_world"`
		AccountChange json.RawMessage `json:"account_change"`
		Update        json.RawMessage `json:"business_intelligence_update"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	for name, doc := range map[string]json.RawMessage{"replay_world": result.ReplayWorld, "account_change": result.AccountChange, "business_intelligence_update": result.Update} {
		if err := v.Validate(name, doc); err != nil {
			t.Fatalf("%s violates its schema: %v\n%s", name, err, doc)
		}
	}
	var change struct {
		GraphDiffRef struct {
			ID string `json:"id"`
		} `json:"graph_diff_ref"`
	}
	_ = json.Unmarshal(result.AccountChange, &change)

	// the graph diff the change points at is the real projector's diff of the released event
	if code, diff := request(t, http.MethodGet, base+"/events/"+change.GraphDiffRef.ID+"/graph-diff", apiToken, ""); code != http.StatusOK || !strings.Contains(diff, `"projected":true`) {
		t.Fatalf("graph diff of the released event = %d %s", code, diff)
	}
	// the existing read endpoint serves the writer's update
	var updated struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(result.Update, &updated)
	if code, latest := request(t, http.MethodGet, base+"/accounts/"+world.Account+"/business-intelligence/latest", apiToken, ""); code != http.StatusOK || !strings.Contains(latest, updated.ID) {
		t.Fatalf("latest update = %d %s", code, latest)
	}

	// idempotent: a second Play changes nothing
	before := count(t, `SELECT (SELECT count(*) FROM source_events) || '/' || (SELECT count(*) FROM account_changes) || '/' || (SELECT count(*) FROM state_history)`)
	if code, again := request(t, http.MethodPost, base+"/replay/play", apiToken, `{"manifest_id":"`+manifest+`"}`); code != http.StatusConflict || !strings.Contains(again, "already_released") {
		t.Fatalf("second Play = %d %s", code, again)
	}
	if after := count(t, `SELECT (SELECT count(*) FROM source_events) || '/' || (SELECT count(*) FROM account_changes) || '/' || (SELECT count(*) FROM state_history)`); after != before {
		t.Fatalf("a second Play changed the database: %s -> %s", before, after)
	}
}
