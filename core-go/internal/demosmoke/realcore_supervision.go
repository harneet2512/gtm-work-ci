package demosmoke

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// checkSupervision is the HAR-120 acceptance: the customer answers the email the approved action
// sent, the supervision scan turns that into a 'replied' customer_reaction on the episode, and the
// knowledge the decision used earns the evidence — support_count and positive_reactions each move
// from 0 to 1 (decision_episode + customer_reaction/positive evidence on a fresh candidate).
func (r *RealCore) checkSupervision(ctx context.Context) error {
	when := time.Now().UTC().Add(2 * time.Second) // strictly after send_decided_at; in the window
	msgID := "demo-reply-" + r.Seed.EpisodeID[:8]
	payload, err := json.Marshal(map[string]any{
		"kind": "email", "message_id": msgID, "thread_id": "demo-thread-" + r.Seed.EpisodeID[:8],
		"in_reply_to": "sent-" + r.Seed.EpisodeID[:8], "direction": "inbound",
		"from": map[string]any{"email": r.Seed.MarcoEmail(), "name": "Marco (seed)"},
		"to":   []map[string]any{{"email": "dana@vendor.example", "name": "Dana"}},
		"cc":   []map[string]any{{"email": r.Seed.PriyaEmail()}},
		"date": when.Format(time.RFC3339Nano), "subject": "Re: Repaired subject",
		// No classifier phrase on purpose ("next steps", "works for us", … each writes a second
		// reaction row): the acceptance asserts exactly one neutral replied row and positive_reactions 0.
		"body_text": "Thanks Dana — confirmed for Thursday on our side.",
	})
	if err != nil {
		return err
	}
	res, err := r.ing.Ingest(ctx, normalize.SourceEvent{
		SourceSystem: "email", SourceObjectID: msgID, SourceEventKey: "received", OccurredAt: &when,
		Origin: "synthetic", Provenance: "synthetic:v1", Payload: payload})
	if err != nil {
		return fmt.Errorf("demosmoke: ingest the customer reply: %w", err)
	}
	if res.AccountID == nil || *res.AccountID != r.Seed.AccountID {
		return fmt.Errorf("demosmoke: the reply did not attribute to the account (account_id = %v)", res.AccountID)
	}
	// In production the coalescer's AfterRecompute hook scans inside the recompute tx; the smoke runs
	// no coalescer, so the scan runs here — the same ScanAccountTx path, committed in its own tx.
	scan, err := r.sup.ScanAccount(ctx, r.Seed.AccountID)
	if err != nil {
		return fmt.Errorf("demosmoke: supervision scan: %w", err)
	}
	if scan.Reactions != 1 {
		return fmt.Errorf("demosmoke: supervision scan wrote %d reactions, want the one reply", scan.Reactions)
	}
	if err := r.readSupervision(ctx); err != nil {
		return err
	}
	var support, positive int
	if err := r.db.QueryRow(`SELECT support_count, (counts->>'positive_reactions')::int FROM knowledge WHERE id = $1::uuid`,
		r.Seed.KnowledgeID).Scan(&support, &positive); err != nil {
		return fmt.Errorf("demosmoke: read the knowledge counts: %w", err)
	}
	if support != 1 || positive != 0 {
		return fmt.Errorf("demosmoke: the reply raised support to %d and positive_reactions to %d; want 1 and 0 (a bare reply is not positive)", support, positive)
	}
	var nEvidence int
	if err := r.db.QueryRow(`SELECT count(*)::int FROM knowledge_evidence WHERE knowledge_id = $1::uuid
  AND kind IN ('decision_episode', 'human_decision', 'customer_reaction')`, r.Seed.KnowledgeID).Scan(&nEvidence); err != nil {
		return fmt.Errorf("demosmoke: read knowledge evidence: %w", err)
	}
	if nEvidence != 3 {
		return fmt.Errorf("demosmoke: %d knowledge evidence rows, want decision_episode + human_decision + customer_reaction", nEvidence)
	}
	// Idempotent: a second scan of the same window writes nothing new (the (activity, type) dedupe and
	// knowledge_evidence_once make it a no-op).
	again, err := r.sup.ScanAccount(ctx, r.Seed.AccountID)
	if err != nil {
		return fmt.Errorf("demosmoke: rescan: %w", err)
	}
	if again.Reactions != 0 || again.Outcomes != 0 || again.Evidence != 0 {
		return fmt.Errorf("demosmoke: rescan wrote %s, want a no-op", again)
	}
	return nil
}

// readSupervision calls GET /episodes/{id}/reactions on the real API and checks the reply's row.
func (r *RealCore) readSupervision(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL+"/episodes/"+r.Seed.EpisodeID+"/reactions", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("demosmoke: get episode reactions: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("demosmoke: GET /episodes/%s/reactions = %d %s", r.Seed.EpisodeID, resp.StatusCode, body)
	}
	var doc struct {
		CustomerReactions []struct {
			ReactionType string `json:"reaction_type"`
			Polarity     string `json:"polarity"`
			ActivityID   string `json:"activity_id"`
		} `json:"customer_reactions"`
		BusinessOutcomes []any `json:"business_outcomes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("demosmoke: decode supervision: %w", err)
	}
	if doc.BusinessOutcomes == nil {
		return errors.New("demosmoke: business_outcomes is null, want the empty array the contract requires")
	}
	if len(doc.CustomerReactions) != 1 || doc.CustomerReactions[0].ReactionType != "replied" || doc.CustomerReactions[0].Polarity != "neutral" {
		return fmt.Errorf("demosmoke: supervision returned %+v, want the one neutral replied reaction", doc.CustomerReactions)
	}
	return nil
}
