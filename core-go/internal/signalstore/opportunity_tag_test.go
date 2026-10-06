package signalstore_test

import (
	"context"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/signals"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// A signal can be about a deal other than the batch's scope: a deal that closed is no longer the primary the
// batch is scoped to, and its stage signal must carry its own opportunity_id.
func TestASignalAboutAClosedDealCarriesItsOwnOpportunity(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	primary := scalar(t, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, 'Renewal', 'renewal') RETURNING id::text`, f.account)
	closed := scalar(t, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, 'EU rollout', 'expansion') RETURNING id::text`, f.account)
	id, _, err := signalstore.InsertDiff(ctx, env.DB, diff(f.account, 1), t0)
	if err != nil {
		t.Fatal(err)
	}
	exp := t0.Add(signals.EventWindow)
	sc := signalstore.Scope{AccountID: f.account, OpportunityID: primary, StateDiffID: id, CreatedAt: t0}
	in := []signals.Signal{
		{Type: "stage_advanced", Rule: "sig.deal_closed@1", OpportunityID: closed, OccurredAt: t0, ExpiresAt: &exp, DedupeKey: "closed"},
		{Type: "customer_replied", Rule: "sig.customer_replied@1", OccurredAt: t0, ExpiresAt: &exp, DedupeKey: "reply"},
	}
	if _, err := signalstore.InsertSignals(ctx, env.DB, sc, in); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, `SELECT opportunity_id::text FROM signals WHERE dedupe_key = 'closed'`); got != closed {
		t.Errorf("the closed deal's signal is tagged %s, want %s", got, closed)
	}
	if got := scalar(t, `SELECT opportunity_id::text FROM signals WHERE dedupe_key = 'reply'`); got != primary {
		t.Errorf("a signal without its own deal takes the batch's scope: %s, want %s", got, primary)
	}
}
