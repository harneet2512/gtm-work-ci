package dealbench

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
)

const reportRel = "bench/reports/opportunity-state-2026-10-03.json"

func repoPath(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "contracts", "schemas")); err == nil {
			return filepath.Join(dir, filepath.FromSlash(rel))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}

func measure(t *testing.T) Report {
	t.Helper()
	snap, err := crmarena.Load(repoPath(t, "fixtures/crmarena_sample"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Measure(snap)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestPerDealStateMatchesTheCRMDealItBelongsTo is the ADR-0016 measurement: every deal's owner, stage and
// amount equal that deal's CRM record, where the account-wide fold matched only a fraction of them.
func TestPerDealStateMatchesTheCRMDealItBelongsTo(t *testing.T) {
	r := measure(t)
	t.Logf("accounts=%d deals=%d (%.2f per account) claims=%d", r.Accounts, r.Deals, r.DealsPerAccount, r.Claims)
	t.Logf("account-wide (before): owner %d/%d = %.4f, stage %d/%d = %.4f",
		r.AccountWideBefore.Owner.Match, r.AccountWideBefore.Owner.Of, r.AccountWideBefore.Owner.Rate,
		r.AccountWideBefore.Stage.Match, r.AccountWideBefore.Stage.Of, r.AccountWideBefore.Stage.Rate)
	t.Logf("per deal (after):      owner %d/%d = %.4f, stage %d/%d = %.4f, amount %d/%d = %.4f",
		r.PerDealAfter.Owner.Match, r.PerDealAfter.Owner.Of, r.PerDealAfter.Owner.Rate,
		r.PerDealAfter.Stage.Match, r.PerDealAfter.Stage.Of, r.PerDealAfter.Stage.Rate,
		r.PerDealAfter.Amount.Match, r.PerDealAfter.Amount.Of, r.PerDealAfter.Amount.Rate)
	t.Logf("account headline matches its primary deal: %d/%d; accounts without an open deal: %d",
		r.HeadlineMatchesPrimary.Match, r.HeadlineMatchesPrimary.Of, r.AccountsWithoutOpen)

	if r.Deals < 20 || r.Accounts != 3 {
		t.Fatalf("the sample is 3 accounts and 26 deals, measured %d/%d", r.Accounts, r.Deals)
	}
	for name, s := range map[string]Score{"owner": r.PerDealAfter.Owner, "stage": r.PerDealAfter.Stage, "amount": r.PerDealAfter.Amount} {
		if s.Of == 0 || s.Match != s.Of {
			t.Errorf("per-deal %s = %d/%d, want every deal to match its CRM record", name, s.Match, s.Of)
		}
	}
	if w := r.AccountWideBefore; w.Owner.Rate >= 0.6 || w.Stage.Rate >= 0.6 {
		t.Errorf("the account-wide fold was expected to mismatch most deals, got owner %.2f stage %.2f", w.Owner.Rate, w.Stage.Rate)
	}
	if r.HeadlineMatchesPrimary.Of == 0 || r.HeadlineMatchesPrimary.Match != r.HeadlineMatchesPrimary.Of {
		t.Errorf("the account headline must be its primary deal's: %+v", r.HeadlineMatchesPrimary)
	}
}

// TestCommittedReportIsCurrent keeps bench/reports honest: the measurement is deterministic, so the committed
// report must equal a fresh run. UPDATE_DEALBENCH=1 rewrites it.
func TestCommittedReportIsCurrent(t *testing.T) {
	want, err := json.MarshalIndent(measure(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	path := repoPath(t, reportRel)
	if os.Getenv("UPDATE_DEALBENCH") == "1" {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run with UPDATE_DEALBENCH=1 to create it): %v", reportRel, err)
	}
	if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
		t.Fatalf("%s is stale; run UPDATE_DEALBENCH=1 go test ./internal/dealbench\nwant:\n%s", reportRel, want)
	}
}
