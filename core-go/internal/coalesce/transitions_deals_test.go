package coalesce_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

// dealClaim stores a claim tagged with an opportunity (its activity is tagged too), like a connector that ties activity to a deal.
func (w *reorgWorld) dealClaim(t *testing.T, opp string, path claims.FieldPath, value, subject string, at time.Time) string {
	t.Helper()
	w.n++
	act := newActivity(t, w.account, w.n, at)
	if _, err := env.DB.Exec(`UPDATE activities SET opportunity_id = $1::uuid WHERE id = $2::uuid`, opp, act); err != nil {
		t.Fatal(err)
	}
	c := claims.Claim{AccountID: w.account, OpportunityID: opp, FieldPath: path, Value: json.RawMessage(value), SubjectPersonID: subject,
		Standing: claims.FirstPartyAI, Confidence: 0.9, SourceActivityID: act, EvidenceQuote: "a verbatim quote", OccurredAt: at,
		Extractor: "test@1", Status: claims.StatusActive}
	if _, err := claimstore.InsertClaims(context.Background(), env.DB, []claims.Claim{c}); err != nil {
		t.Fatal(err)
	}
	return act
}

func (w *reorgWorld) deal(t *testing.T, name string) string {
	t.Helper()
	return scalar(t, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, $2, 'expansion') RETURNING id::text`, w.account, name)
}

// ADR-0016: the account headline is the primary deal. A newer deal with an unsettled owner becomes primary, but
// the expansion that deal A has earned must not be reversed (or missed) because the headline moved.
func TestAChangeOfPrimaryDealCannotHideOrReverseAnEarnedExpansion(t *testing.T) {
	w := newReorgWorld(t, true)
	w.earnReorg(t)
	a, b := w.deal(t, "EU rollout"), w.deal(t, "APAC pilot")

	w.dealClaim(t, a, claims.FieldChampion, w.person(w.marco), w.marco, day(8))
	w.dealClaim(t, a, claims.FieldChampionStatus, `"active"`, "", day(8))
	w.dealClaim(t, a, "expansion_need", `"Roll out to the EU plants"`, "", day(9))
	w.dealClaim(t, a, claims.FieldBuyingGroupMember, `{"role":"user"}`, w.owen, day(9))
	w.dealClaim(t, a, "business_value", `"Cut downtime 18% at the pilot plant"`, "", day(30))
	w.dealClaim(t, a, claims.FieldEconomicBuyer, w.person(w.owen), w.owen, day(30))
	w.dealClaim(t, a, claims.FieldDecisionProcess, `"budget review on Oct 12"`, "", day(40))
	// The newest evidence is on deal B, whose champion is weakening: B is now the primary deal.
	w.dealClaim(t, b, claims.FieldChampion, w.person(w.priya), w.priya, day(41))
	trigger := w.dealClaim(t, b, claims.FieldChampionStatus, `"weakening"`, "", day(41))
	w.recompute(t, day(42), trigger)

	raw := scalar(t, `SELECT state->'fields'->'champion_status'->>'value' || '/' || (state->>'opportunity_id') FROM account_state WHERE account_id = $1::uuid`, w.account)
	if raw != "weakening/"+b {
		t.Fatalf("headline = %s, want deal B's weakening champion (the primary deal)", raw)
	}
	e := w.exposed(t)
	if e.RelationshipState.Value != "EXPANSION" {
		t.Fatalf("exposed = %+v, want EXPANSION earned on deal A although deal B is primary", e)
	}
	rows := w.transitions(t)
	if rows[0].Status != transitions.StatusConfirmed || *rows[0].ToStateCandidate != "EXPANSION" {
		t.Fatalf("transitions[0] = %+v", rows[0])
	}
}

// A retraction is evidence of the opposite: "the rollout is off" ends the candidate the earlier need opened.
func TestARetractedExpansionNeedRejectsTheCandidate(t *testing.T) {
	w := newReorgWorld(t, true)
	w.earnReorg(t)
	w.claim(t, claims.FieldChampion, w.person(w.marco), w.marco, day(8), claims.FirstPartyAI)
	w.claim(t, "expansion_need", `"Roll out to the EU plants"`, "", day(9), claims.FirstPartyAI)
	trigger := w.claim(t, claims.FieldBuyingGroupMember, `{"role":"user"}`, w.owen, day(9), claims.FirstPartyAI)
	w.recompute(t, day(10), trigger)
	if rows := w.transitions(t); rows[0].Status != transitions.StatusCandidate {
		t.Fatalf("setup: %+v", rows[0])
	}

	off := w.claim(t, "expansion_need", `{"stance":"withdrawn","text":"the rollout is off"}`, "", day(12), claims.FirstPartyAI)
	w.recompute(t, day(13), off)
	rows := w.transitions(t)
	if rows[0].Status != transitions.StatusRejected {
		t.Fatalf("transitions[0] = %+v, want REJECTED by the retraction", rows[0])
	}
	if e := w.exposed(t); e.RelationshipState.Value != "REORG" || e.OpenTransition != nil {
		t.Errorf("exposed = %+v, want REORG with nothing open", e)
	}
}
