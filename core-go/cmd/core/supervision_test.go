package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// TestSupervisionHookScansInsideTheRecompute: the WP22 acceptance path through the real wiring — a
// seeded sent episode, an ingested customer reply, the coalescer's recompute transaction, and the
// supervision hook scanning inside it (HAR-120). Asserts the reaction row exists once the recompute
// commits — the hook is the only writer, so a row proves the scan rode the transaction.
func TestSupervisionHookScansInsideTheRecompute(t *testing.T) {
	ctx := context.Background()
	seed := strategytest.Seed(t, env.DB, ctxfixture.Get(t, env.DB).AccountA)
	logger, _ := quietLogger()
	strategies, err := strategystore.New(env.DB, logger)
	if err != nil {
		t.Fatalf("strategystore.New: %v", err)
	}
	if _, _, err := strategies.RecordDecision(ctx, seed.RunID, strategystore.DecisionRequest{
		SelectedCandidateID: seed.Candidates[1], Surface: "api", ActorLabel: "supervision-test",
	}); err != nil {
		t.Fatalf("record decision: %v", err)
	}
	if _, err := strategies.Send(ctx, seed.RunID, strategystore.SendRequest{
		Decision: "send", Surface: "api", ActorLabel: "supervision-test",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	cfg := testConfig("127.0.0.1:0")
	cfg.WorkerURL = ""
	cfg.KnowledgeRulesPath = repoPath(t, "contracts/knowledge/lifecycle.v1.json")
	base, stop := startCore(t, cfg)
	defer func() { _ = stop() }()

	// An inbound reply from Marco — a send recipient, so it links on authorship alone (scan.go links).
	reply := fmt.Sprintf(`{"source_system":"email","source_object_id":"sup-reply-1","source_event_key":"received","payload":{
	 "kind":"email","message_id":"sup-reply-1","thread_id":"sup-t-1","direction":"inbound",
	 "from":{"email":%q,"name":"Marco"},"to":[{"email":"dana@ghostvendor.com"}],
	 "date":%q,"subject":"re: hi","body_text":"Confirmed for Thursday."}}`,
		seed.MarcoEmail(), time.Now().Add(time.Minute).UTC().Format(time.RFC3339))
	if code, body := request(t, http.MethodPost, base+"/ingest", apiToken, reply); code != http.StatusCreated {
		t.Fatalf("ingest = %d %s", code, body)
	}

	deadline := time.Now().Add(20 * time.Second)
	var reaction string
	for time.Now().Before(deadline) {
		err := env.DB.QueryRow(`SELECT reaction_type FROM customer_reactions WHERE decision_episode_id = $1::uuid`,
			seed.EpisodeID).Scan(&reaction)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if reaction != "replied" {
		t.Fatalf("customer_reactions = %q, want the hook's scan to have written 'replied'", reaction)
	}
}
