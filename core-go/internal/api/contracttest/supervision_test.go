package contracttest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// TestEpisodeReactionsConformToTheContract exercises GET /episodes/{id}/reactions end to end: a
// seeded episode is decided and sent over HTTP, a customer reply comes in through the real ingest,
// the scan detects it, and every response is checked against core.yaml.
func TestEpisodeReactionsConformToTheContract(t *testing.T) {
	s := newStack(t)
	seed := strategytest.Seed(t, env.DB, s.world.AccountA)
	run, episode := "/runs/"+seed.RunID, "/episodes/"+seed.EpisodeID
	path := "/episodes/{episode_id}/reactions"

	// Before the send the endpoint answers 200 with empty arrays — the episode exists, nothing to see.
	if r := s.get(episode+"/reactions", path); r.status != http.StatusOK {
		t.Fatalf("supervision before send: %d %s", r.status, clip(r.body))
	} else {
		var doc struct {
			CustomerReactions []any `json:"customer_reactions"`
			BusinessOutcomes  []any `json:"business_outcomes"`
		}
		if err := json.Unmarshal(r.body, &doc); err != nil || doc.CustomerReactions == nil || doc.BusinessOutcomes == nil {
			t.Fatalf("empty supervision must be two empty arrays: %s %v", clip(r.body), err)
		}
	}

	choose := map[string]any{"selected_candidate_id": seed.Candidates[1], "surface": "api", "actor_label": "op"}
	if r := s.post(run+"/strategy-decision", "/runs/{run_id}/strategy-decision", choose); r.status != 201 {
		t.Fatalf("choose: %d %s", r.status, clip(r.body))
	}
	send := map[string]any{"decision": "send", "surface": "api", "actor_label": "op"}
	if r := s.post(run+"/send", "/runs/{run_id}/send", send); r.status != 200 {
		t.Fatalf("send: %d %s", r.status, clip(r.body))
	}

	// A customer reply through the real ingest: Marco (the sent recipient) answers the sent mail.
	when := time.Now().UTC().Add(2 * time.Second)
	payload, _ := json.Marshal(map[string]any{
		"kind": "email", "message_id": "ct-reply-" + seed.EpisodeID[:8],
		"thread_id": "ct-thread", "in_reply_to": "sent", "direction": "inbound",
		"from": map[string]any{"email": seed.MarcoEmail(), "name": "Marco"},
		"to":   []map[string]any{{"email": "dana@ghostvendor.com"}},
		"date": when.Format(time.RFC3339Nano), "subject": "Re: hello",
		"body_text": "Thanks Dana — confirmed for Thursday.",
	})
	res, err := s.ing.Ingest(context.Background(), normalize.SourceEvent{
		SourceSystem: "email", SourceObjectID: "ct-reply-" + seed.EpisodeID[:8],
		SourceEventKey: "received", OccurredAt: &when, Origin: "synthetic",
		Provenance: "synthetic:v1", Payload: payload})
	if err != nil {
		t.Fatalf("ingest the reply: %v", err)
	}
	if res.AccountID == nil || *res.AccountID != seed.AccountID {
		t.Fatalf("the reply did not attribute to the account: %v", res.AccountID)
	}
	if _, err := s.sup.ScanAccount(context.Background(), seed.AccountID); err != nil {
		t.Fatalf("scan: %v", err)
	}

	r := s.get(episode+"/reactions", path)
	if r.status != http.StatusOK {
		t.Fatalf("supervision: %d %s", r.status, clip(r.body))
	}
	var doc struct {
		CustomerReactions []struct {
			ReactionType string `json:"reaction_type"`
			Polarity     string `json:"polarity"`
			ActivityID   string `json:"activity_id"`
		} `json:"customer_reactions"`
		BusinessOutcomes []any `json:"business_outcomes"`
	}
	if err := json.Unmarshal(r.body, &doc); err != nil {
		t.Fatalf("decode supervision: %v", err)
	}
	if len(doc.CustomerReactions) != 1 || doc.CustomerReactions[0].ReactionType != "replied" || doc.CustomerReactions[0].Polarity != "positive" {
		t.Fatalf("reactions = %+v, want the one positive replied", doc.CustomerReactions)
	}
	if res.ActivityID != doc.CustomerReactions[0].ActivityID {
		t.Fatalf("reaction activity %s, want the ingested reply %s", doc.CustomerReactions[0].ActivityID, res.ActivityID)
	}
	if doc.BusinessOutcomes == nil || len(doc.BusinessOutcomes) != 0 {
		t.Fatal("business_outcomes must be the empty array")
	}

	// Error paths: unknown and malformed ids, no auth, wrong method.
	s.expectError(s.get("/episodes/"+missingID+"/reactions", path), http.StatusNotFound, "not_found")
	s.expectError(s.get("/episodes/not-a-uuid/reactions", path), http.StatusNotFound, "not_found")
	if r := s.do("GET", episode+"/reactions", path, "", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("no token: %d", r.status)
	}
	if r := s.do("POST", episode+"/reactions", "", apiToken, []byte(`{}`)); r.status != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d, want 405", r.status)
	}
}
