package claimstore

import (
	"context"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// settle stores the statuses adjudication gives the claims as of now, the way a recompute does.
func settle(t *testing.T, f fixture, now time.Time) {
	t.Helper()
	all, err := LoadAccountClaims(context.Background(), env.DB, f.account)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyUpdates(context.Background(), env.DB, claims.Adjudicate(all, now).Updates); err != nil {
		t.Fatal(err)
	}
}

func claimID(t *testing.T, value string) string {
	t.Helper()
	return scalarQuery(t, `SELECT id::text FROM claims WHERE value = to_jsonb($1::text)`, value)
}

// twoMoments gives the second activity its own, later time, so claims can be made at t0 and t0+1h.
func twoMoments(t *testing.T, f fixture) (first, second time.Time) {
	t.Helper()
	first, second = t0, t0.Add(time.Hour)
	if _, err := env.DB.Exec(`UPDATE activities SET occurred_at = $2 WHERE id = $1::uuid`, f.otherActivity, second); err != nil {
		t.Fatal(err)
	}
	return first, second
}

func insert(t *testing.T, cs ...claims.Claim) {
	t.Helper()
	if _, err := InsertClaims(context.Background(), env.DB, cs); err != nil {
		t.Fatal(err)
	}
}

func TestClaimsAsOfUndoesASupersessionMadeAtOrAfterT(t *testing.T) {
	f := seed(t)
	first, second := twoMoments(t, f)
	old, replacement := aiClaim(f, claims.FieldStage, `"Discovery"`, first), aiClaim(f, claims.FieldStage, `"Negotiation"`, second)
	replacement.SourceActivityID = f.otherActivity
	insert(t, old, replacement)
	settle(t, f, second.Add(time.Hour))
	oldID, newID := claimID(t, "Discovery"), claimID(t, "Negotiation")
	if got := scalarQuery(t, `SELECT status FROM claims WHERE id = $1::uuid`, oldID); got != "superseded" {
		t.Fatalf("precondition: the stored status is %s", got)
	}

	for _, tc := range []struct {
		name     string
		at       time.Time
		oldState claims.Status
		newState claims.Status // "" = not visible
		by       string
	}{
		{"before everything", first, "", "", ""},
		{"between: the old claim stands, the new one does not exist", first.Add(30 * time.Minute), claims.StatusActive, "", ""},
		{"exactly at the replacing claim: strict, it is not before itself", second, claims.StatusActive, "", ""},
		{"after: superseded by the replacement", second.Add(time.Microsecond), claims.StatusSuperseded, claims.StatusActive, newID},
	} {
		got, err := ClaimsAsOf(context.Background(), env.DB, f.account, tc.at)
		if err != nil {
			t.Fatal(err)
		}
		check := func(id string, want claims.Status) {
			c, ok := got[id]
			if (want == "") == ok || (ok && c.Status != want) {
				t.Errorf("%s: claim %s = %+v (visible %v), want %q", tc.name, id, c, ok, want)
			}
		}
		check(oldID, tc.oldState)
		check(newID, tc.newState)
		if tc.by != "" {
			c := got[oldID]
			if c.SupersededBy != tc.by || c.SupersededAt == nil || !c.SupersededAt.Equal(second) {
				t.Errorf("%s: supersession = %+v, want by %s at %s", tc.name, c, tc.by, second)
			}
		}
	}
}

// An outranked claim carries no superseded_by pointer, so only re-adjudication can say it stood earlier.
func TestClaimsAsOfRestoresAClaimThatWasOutrankedLater(t *testing.T) {
	f := seed(t)
	first, second := twoMoments(t, f)
	weak, strong := aiClaim(f, claims.FieldStage, `"Discovery"`, first), aiClaim(f, claims.FieldStage, `"Closed"`, second)
	strong.Standing, strong.Confidence, strong.SourceActivityID = claims.CRMExplicit, 1, f.otherActivity
	insert(t, weak, strong)
	settle(t, f, second.Add(time.Hour))
	weakID := claimID(t, "Discovery")
	if got := scalarQuery(t, `SELECT status || ':' || (superseded_by IS NULL)::text FROM claims WHERE id = $1::uuid`, weakID); got != "outranked:true" {
		t.Fatalf("precondition: %s", got)
	}
	got, err := ClaimsAsOf(context.Background(), env.DB, f.account, first.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := got[weakID]; !ok || c.Status != claims.StatusActive {
		t.Fatalf("before the outranking claim existed this one stood: %+v ok=%v", c, ok)
	}
}

func TestClaimsAsOfExpiresAtTheExpiryAndNeverEarlier(t *testing.T) {
	f := seed(t)
	first, _ := twoMoments(t, f)
	c := aiClaim(f, claims.FieldHealth, `"at_risk"`, first)
	expiry := first.Add(2 * time.Hour)
	c.ExpiresAt = &expiry
	insert(t, c)
	id := claimID(t, "at_risk")
	for _, tc := range []struct {
		at   time.Time
		want claims.Status
	}{{expiry.Add(-time.Minute), claims.StatusActive}, {expiry, claims.StatusActive}, {expiry.Add(time.Microsecond), claims.StatusExpired}} {
		got, err := ClaimsAsOf(context.Background(), env.DB, f.account, tc.at)
		if err != nil || got[id].Status != tc.want {
			t.Errorf("at %s: %+v err=%v, want %s", tc.at, got[id], err, tc.want)
		}
	}
}

// A human rejection has no world time: a rejected claim is in no as-of answer, as neither "rejected" nor "active" is true at T.
func TestClaimsAsOfLeavesOutRejectedClaimsAndClaimsOfLaterActivities(t *testing.T) {
	f := seed(t)
	first, second := twoMoments(t, f)
	kept, rejected, late := aiClaim(f, claims.FieldHealth, `"on_track"`, first), aiClaim(f, claims.FieldBlockers, `"budget"`, first), aiClaim(f, claims.FieldSummary, `"later"`, second)
	late.SourceActivityID = f.otherActivity
	insert(t, kept, rejected, late)
	if _, err := env.DB.Exec(`UPDATE claims SET status = 'rejected' WHERE value = to_jsonb('budget'::text)`); err != nil {
		t.Fatal(err)
	}
	got, err := ClaimsAsOf(context.Background(), env.DB, f.account, first.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[claimID(t, "on_track")].Status != claims.StatusActive {
		t.Fatalf("want only the kept claim, got %+v", got)
	}
}

func TestClaimsAsOfRejectsABadAccountID(t *testing.T) {
	seed(t)
	if _, err := ClaimsAsOf(context.Background(), env.DB, "not-a-uuid", t0); err == nil {
		t.Fatal("a bad account id must be an error")
	}
}
