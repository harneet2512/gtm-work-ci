package coalesce_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstest"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// Scenario assertions of the gold acceptance test (ADR-0008 conflicts, retained losers, enrichment, delegation).

type claimRow struct{ standing, status, activity, field string }

func claimByID(t *testing.T, id string) claimRow {
	t.Helper()
	var c claimRow
	if err := env.DB.QueryRow(`SELECT standing, status, source_activity_id::text, field_path FROM claims WHERE id = $1::uuid`, id).Scan(&c.standing, &c.status, &c.activity, &c.field); err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	return c
}

// assertConflicts: the set of state fields carrying conflicts equals the gold's conflicts at every
// checkpoint, each conflict names the right claims and standings, and the winner stands.
func (r *run) assertConflicts(t *testing.T) {
	total := 0
	for _, res := range r.results {
		st := r.parsedState(t, res.gold.Account, res.gold.Checkpoint)
		got := map[string]bool{}
		for _, name := range reducer.FieldNames() {
			if len(st.Fields.Field(name).Conflicts) > 0 {
				got[name] = true
			}
		}
		want := map[string]bool{}
		for _, gc := range res.gold.Expected.Conflicts {
			want[gc.Field] = true
		}
		if fmt.Sprint(sortedKeys(got)) != fmt.Sprint(sortedKeys(want)) {
			t.Errorf("%s cp%d: fields with conflicts = %v, gold = %v", res.gold.Account, res.gold.Checkpoint, sortedKeys(got), sortedKeys(want))
		}
		hookFields := map[string]bool{}
		for _, c := range res.recompute.Conflicts {
			hookFields[string(c.Field)] = true
		}
		if fmt.Sprint(sortedKeys(hookFields)) != fmt.Sprint(sortedKeys(want)) {
			t.Errorf("%s cp%d: the hook payload carries conflicts for %v, gold = %v", res.gold.Account, res.gold.Checkpoint, sortedKeys(hookFields), sortedKeys(want))
		}
		for _, gc := range res.gold.Expected.Conflicts {
			total++
			f := r.fieldOf(st, gc.Field)
			if len(f.Conflicts) != 1 || f.WinningClaimID == nil {
				t.Errorf("%s cp%d %s: field = %+v", res.gold.Account, res.gold.Checkpoint, gc.Field, f)
				continue
			}
			winner, contra := claimByID(t, *f.WinningClaimID), claimByID(t, f.Conflicts[0].ClaimID)
			if winner.standing != gc.WinnerStanding || winner.activity != r.activity[gc.WinnerEventFile] {
				t.Errorf("winner = %+v, gold = %s from %s", winner, gc.WinnerStanding, gc.WinnerEventFile)
			}
			if contra.standing != gc.ContradictingStanding || f.Conflicts[0].Standing != gc.ContradictingStanding || contra.activity != r.activity[gc.ContradictingEventFile] {
				t.Errorf("contradicting claim = %+v (%s), gold = %s from %s", contra, f.Conflicts[0].Standing, gc.ContradictingStanding, gc.ContradictingEventFile)
			}
			if norm(fmt.Sprint(f.Value)) != norm(strings.Trim(string(gc.WinnerValue), `"`)) {
				t.Errorf("the winner must stand: value = %v, gold winner_value = %s", f.Value, gc.WinnerValue)
			}
			if contra.status != string(claims.StatusOutranked) {
				t.Errorf("the contradicting claim must be retained as outranked, got %s", contra.status)
			}
			inCompeting := false
			for _, id := range f.CompetingClaimIDs {
				inCompeting = inCompeting || id == f.Conflicts[0].ClaimID
			}
			if !inCompeting {
				t.Errorf("conflicting claim missing from competing_claim_ids")
			}
			hooked := false
			for _, c := range res.recompute.Conflicts {
				hooked = hooked || (c.Contradicting.ID == f.Conflicts[0].ClaimID && c.Winner.ID == *f.WinningClaimID)
			}
			if !hooked {
				t.Errorf("the hook payload lacks the conflict between %s and %s", *f.WinningClaimID, f.Conflicts[0].ClaimID)
			}
		}
	}
	if total != 2 {
		t.Errorf("gold declares %d conflicts across the checkpoints, want 2 (Beta cp3 and cp4)", total)
	}
}

func norm(s string) string { return claimstest.NormalizeText(s) }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (r *run) assertOutrankedRetained(t *testing.T) {
	acme := r.acctID(t, "acme")
	var status, supersededBy string
	err := env.DB.QueryRow(`SELECT status, COALESCE(superseded_by::text, '') FROM claims WHERE account_id = $1::uuid AND field_path = 'stage' AND value = '"Negotiation"'::jsonb`, acme).Scan(&status, &supersededBy)
	if err != nil {
		t.Fatalf("the stale AI stage claim was not retained: %v", err)
	}
	if status != "outranked" {
		t.Fatalf("stale AI claim 'Negotiation' status = %s, want outranked", status)
	}
	for cp, want := range map[int]string{2: "Commercial review", 3: "Technical evaluation", 4: "Technical evaluation"} {
		st := r.parsedState(t, "acme", cp)
		f := st.Fields.Stage
		if f.Value != want || f.Standing == nil || *f.Standing != "crm_explicit" || len(f.CompetingClaimIDs) == 0 {
			t.Errorf("acme cp%d stage = %+v, want CRM %q with the AI claim as a competitor", cp, f, want)
		}
	}
	// The rep's never-accepted Oct 1 proposal (an AI next_meeting claim) is outranked by the calendar's known-absent.
	var oct string
	if err := env.DB.QueryRow(`SELECT status FROM claims WHERE account_id = $1::uuid AND field_path = 'next_meeting' AND value::text LIKE '%2026-10-01%'`, acme).Scan(&oct); err != nil || oct != "outranked" {
		t.Errorf("the unaccepted Oct 1 proposal: status=%q err=%v, want outranked", oct, err)
	}
}

func (r *run) acctID(t *testing.T, name string) string {
	t.Helper()
	return scalar(t, `SELECT id::text FROM accounts WHERE lower(name) LIKE $1`, name+" %")
}

func (r *run) assertEnrichment(t *testing.T) {
	beta := r.acctID(t, "beta")
	ravi := r.personID["person:ravi_menon"]
	var status, standing string
	if err := env.DB.QueryRow(`SELECT status, standing FROM claims WHERE account_id = $1::uuid AND subject_person_id = $2::uuid AND standing = 'third_party'`, beta, ravi).Scan(&status, &standing); err != nil {
		t.Fatalf("the enrichment claim about Ravi was not retained: %v", err)
	}
	if status != "outranked" {
		t.Fatalf("enrichment claim status = %s, want outranked by the first-party title", status)
	}
	for _, cp := range []int{3, 4} {
		st := r.parsedState(t, "beta", cp)
		var found bool
		for _, m := range st.BuyingGroup {
			if m.PersonID != ravi {
				continue
			}
			found = true
			if m.Title == nil || *m.Title != "Head of Platform Engineering" {
				t.Errorf("beta cp%d: Ravi's title = %v, want the first-party CRM title", cp, m.Title)
			}
		}
		if !found {
			t.Errorf("beta cp%d: Ravi is not in the buying group", cp)
		}
	}
}

func (r *run) assertAcmeEconomicBuyer(t *testing.T) {
	for cp := 1; cp <= 4; cp++ {
		st := r.parsedState(t, "acme", cp)
		eb := st.Fields.EconomicBuyer
		if eb.Known || eb.Value != "unknown" || eb.WinningClaimID != nil {
			t.Errorf("acme cp%d economic_buyer = %+v, want unknown", cp, eb)
		}
		if fmt.Sprint(st.CoverageGaps) != "[economic_buyer]" {
			t.Errorf("acme cp%d coverage gaps = %v", cp, st.CoverageGaps)
		}
	}
}

func (r *run) assertNorthstarDelegation(t *testing.T) {
	elena, sam := r.personID["person:elena_vasquez"], r.personID["person:sam_okafor"]
	for _, cp := range []int{3, 4} {
		st := r.parsedState(t, "northstar", cp)
		if st.Fields.Champion.Value != elena {
			t.Errorf("northstar cp%d champion = %v, want Elena (still the champion)", cp, st.Fields.Champion.Value)
		}
		if cp == 3 && st.Fields.ChampionStatus.Value != "delegated" {
			t.Errorf("northstar cp3 champion_status = %v", st.Fields.ChampionStatus.Value)
		}
		var delegated bool
		for _, m := range st.BuyingGroup {
			if m.PersonID == elena && m.DelegatedToPerson != nil && *m.DelegatedToPerson == sam {
				delegated = true
			}
		}
		if !delegated {
			t.Errorf("northstar cp%d: Elena is not delegated to Sam in the buying group", cp)
		}
	}
}

func (r *run) assertDerived(t *testing.T) {
	for _, res := range r.results {
		st := r.parsedState(t, res.gold.Account, res.gold.Checkpoint)
		for _, name := range []string{"last_customer_interaction", "last_meaningful_change"} {
			f := r.fieldOf(st, name)
			if !f.Derived || f.WinningClaimID != nil || (f.Known && len(f.EvidenceRefs) == 0) {
				t.Errorf("%s cp%d %s must be derived with evidence and no winning claim: %+v", res.gold.Account, res.gold.Checkpoint, name, f)
			}
		}
		// Slack ts values carry a sequence fraction (".000100" is 100 microseconds), so compare to the second.
		if !st.AsOf.Truncate(time.Second).Equal(res.gold.AsOf) {
			t.Errorf("%s cp%d as_of = %s, gold as_of = %s", res.gold.Account, res.gold.Checkpoint, st.AsOf, res.gold.AsOf)
		}
		if res.recompute.Version != res.gold.Checkpoint {
			t.Errorf("%s cp%d: state version = %d (one recompute per checkpoint)", res.gold.Account, res.gold.Checkpoint, res.recompute.Version)
		}
	}
}
