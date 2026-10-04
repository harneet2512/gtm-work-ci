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

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/slackfake"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

const (
	purgeSQL = `TRUNCATE outbox_events, relationships, state_history, account_state, recompute_jobs, claims, extraction_cache, unresolved_activities,
		activity_participants, activities, source_events, entity_source_mappings, opportunities, people, accounts RESTART IDENTITY CASCADE`
	e2eRunWait = 60 * time.Second
)

// materialEmail is a customer reply asking for something: the extractor turns it into a blocker, which is a
// material change, so the trigger evaluates eligible and a dry-run AgentRun opens.
const materialEmail = `{"source_system":"email","source_object_id":"e2e-orch-1","source_event_key":"received","payload":{
 "kind":"email","message_id":"e2e-orch-1","thread_id":"t","direction":"inbound",
 "from":{"email":"priya.shah@acme.com","name":"Priya Shah"},"to":[{"email":"dana@ghostvendor.com"}],
 "date":"2026-10-03T10:00:00Z","subject":"Re: rollout","body_text":"We need the SOC2 report before signing. Thanks"}}`

func repoPath(t *testing.T, rel string) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return filepath.Join(dir, rel)
		}
		if filepath.Dir(dir) == dir {
			t.Fatalf("%s not found above the working directory", rel)
		}
		dir = filepath.Dir(dir)
	}
}

// seedAcme creates Acme with three contacts and one rep, each with the email mapping ingest resolves by.
func seedAcme(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	if err := storetest.Purge(ctx, env.DB, purgeSQL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storetest.Purge(context.Background(), env.DB, purgeSQL) })
	var account string
	if err := env.DB.QueryRow(`INSERT INTO accounts (name, domain) VALUES ('Acme Corp', 'acme.com') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	people := []struct{ kind, name, email string }{
		{"employee", "Dana Kim", "dana@ghostvendor.com"}, {"contact", "Priya Shah", "priya.shah@acme.com"},
		{"contact", "Marco Diaz", "marco.diaz@acme.com"}, {"contact", "Lena Wu", "lena.wu@acme.com"},
	}
	for _, p := range people {
		var id string
		var err error
		if p.kind == "employee" {
			err = env.DB.QueryRow(`INSERT INTO people (kind, display_name, primary_email) VALUES ('employee', $1, $2) RETURNING id::text`, p.name, p.email).Scan(&id)
		} else {
			err = env.DB.QueryRow(`INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact', $1, $2, $3::uuid) RETURNING id::text`,
				p.name, p.email, account).Scan(&id)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.DB.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method)
 VALUES ('person', $1::uuid, 'email', $2, 1, 'seed')`, id, p.email); err != nil {
			t.Fatal(err)
		}
	}
	return account
}

// extractorStub answers /v1/extract with one blocker claim. It is the worker's extraction path, not the orchestrator's.
func extractorStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, workerAnswer) }))
	t.Cleanup(srv.Close)
	return srv
}

// orchestratorConfig is a running core with the driver on (fast poll and retry so tests do not sleep).
func orchestratorConfig(t *testing.T, enabled bool) config.Config {
	t.Helper()
	cfg := testConfig("127.0.0.1:0")
	cfg.WorkerURL = extractorStub(t).URL
	cfg.Orchestrator = config.Orchestrator{
		Enabled: enabled, Scope: config.ScopeAll, Concurrency: 1, Poll: 25 * time.Millisecond, Retry: 100 * time.Millisecond, WorkspaceID: "workspace-e2e",
		KnowledgeRulesPath: repoPath(t, "contracts/knowledge/lifecycle.v1.json"), RoutingPath: repoPath(t, "contracts/transitions/routing.v1.json"),
	}
	return cfg
}

func getJSON(t *testing.T, url string, into any) int {
	t.Helper()
	code, body := request(t, http.MethodGet, url, apiToken, "")
	if into != nil && code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), into); err != nil {
			t.Fatalf("GET %s: %v\n%s", url, err, body)
		}
	}
	return code
}

type runView struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Generation struct {
		Phase         string  `json:"phase"`
		Attempt       int     `json:"attempt"`
		Reason        *string `json:"reason"`
		StrategySetID *string `json:"strategy_set_id"`
	} `json:"generation"`
}

func runPhase(t *testing.T, base, runID string) runView {
	t.Helper()
	var v runView
	if code := getJSON(t, base+"/runs/"+runID, &v); code != http.StatusOK {
		t.Fatalf("GET /runs/%s = %d", runID, code)
	}
	return v
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(e2eRunWait)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitForPhase(t *testing.T, base, runID, phase string) runView {
	t.Helper()
	var last runView
	waitFor(t, "run phase "+phase, func() bool { last = runPhase(t, base, runID); return last.Generation.Phase == phase })
	return last
}

// ingestMaterialEvent posts the email and returns the id of the run the trigger opens for the account.
func ingestMaterialEvent(t *testing.T, base, account string) string {
	t.Helper()
	if code, body := request(t, http.MethodPost, base+"/ingest", apiToken, materialEmail); code != http.StatusCreated {
		t.Fatalf("ingest = %d %s", code, body)
	}
	var run string
	waitFor(t, "the trigger to open a run", func() bool {
		return env.DB.QueryRow(`SELECT id::text FROM agent_runs WHERE account_id = $1::uuid`, account).Scan(&run) == nil
	})
	return run
}

// peopleFromDB is the real core client with one override: People reads the account graph, which needs the Neo4j
// projection, and this test runs without it. Everything else (the strategy set, the eval bundles, the outbox) is
// served by the real core over HTTP.
type peopleFromDB struct {
	*slacksurface.CoreHTTP
	account string
}

func (p peopleFromDB) People(context.Context, string) (slacksurface.Directory, error) {
	rows, err := env.DB.Query(`SELECT id::text, display_name, COALESCE(primary_email, '') FROM people WHERE account_id = $1::uuid OR kind = 'employee'`, p.account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dir := slacksurface.Directory{}
	for rows.Next() {
		var s slacksurface.Person
		if err := rows.Scan(&s.ID, &s.Name, &s.Email); err != nil {
			return nil, err
		}
		dir[s.ID] = s
	}
	return dir, rows.Err()
}

// relayTo wires the Slack relay to the running core and a fake Slack.
func relayTo(t *testing.T, base, account string) (*slacksurface.Relay, *slackfake.Server) {
	t.Helper()
	fs := slackfake.New()
	t.Cleanup(fs.Close)
	cfg := slacksurface.Config{AppToken: "xapp-t", BotToken: "xoxb-t", ChannelID: "C1", CoreURL: base, APIToken: slacksurface.Secret(apiToken)}
	core := slacksurface.NewCoreHTTP(base, cfg.APIToken, nil)
	api, _ := slacksurface.NewSlackClients(cfg, fs.APIURL())
	logger, _ := quietLogger()
	pub := slacksurface.NewPublisher(peopleFromDB{CoreHTTP: core, account: account}, slacksurface.NewSlackPoster(api), cfg.ChannelID, "")
	relay, err := slacksurface.NewRelay(core, pub, logger, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return relay, fs
}

// chooserCards parses a posted Message 2: the candidate ids of its cards and the evals line of each.
func chooserCards(t *testing.T, blocks json.RawMessage) (ids []string, evals map[string]string) {
	t.Helper()
	var bs []struct {
		BlockID  string `json:"block_id"`
		Type     string `json:"type"`
		Elements []struct {
			Text json.RawMessage `json:"text"` // a string, or a text object, depending on the element
		} `json:"elements"`
	}
	if err := json.Unmarshal(blocks, &bs); err != nil {
		t.Fatalf("blocks: %v", err)
	}
	evals = map[string]string{}
	const card, suffix = "ghost.strategy.card.", ".evals"
	for _, b := range bs {
		if !strings.HasPrefix(b.BlockID, card) || !strings.HasSuffix(b.BlockID, suffix) {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(b.BlockID, card), suffix)
		ids = append(ids, id)
		for _, e := range b.Elements {
			evals[id] += textOf(e.Text)
		}
	}
	return ids, evals
}

// textOf is the text of a Slack text object or plain string.
func textOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &o)
	return o.Text
}
