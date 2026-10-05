package demorun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demomine"
)

// MedTechOpportunity is the default demo case: MedTech Advances, the mined report's top case (HAR-129: rank 1,
// score 15.75, decision_change true). Pioneer Envisions (006Wt000007BF69IAG) is the second, knowledge-transfer case.
const MedTechOpportunity = "006Wt000007BHzBIAW"

// CaseRef is a mined case, resolved from the demo-cases report.
type CaseRef struct {
	Rank            int
	AccountName     string
	OpportunityID   string
	OpportunityName string
	HeldOutEventID  string
}

// LoadReport reads a demo-cases report.
func LoadReport(path string) (demomine.Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return demomine.Report{}, fmt.Errorf("demorun: read the demo-cases report %s: %w", path, err)
	}
	var r demomine.Report
	if err := json.Unmarshal(b, &r); err != nil {
		return demomine.Report{}, fmt.Errorf("demorun: %s is not a demo-cases report: %w", path, err)
	}
	return r, nil
}

// PickCase finds the case for an opportunity in the report's top cases, then in the full ranking.
func PickCase(rep demomine.Report, opportunityID string) (CaseRef, error) {
	for _, c := range rep.Top {
		if c.OpportunityID == opportunityID {
			return CaseRef{Rank: c.Rank, AccountName: c.AccountName, OpportunityID: c.OpportunityID,
				OpportunityName: c.OpportunityName, HeldOutEventID: c.HeldOut.EventID}, nil
		}
	}
	for _, b := range rep.Ranking {
		if b.OpportunityID == opportunityID {
			return CaseRef{Rank: b.Rank, OpportunityID: b.OpportunityID, AccountName: b.OpportunityID, HeldOutEventID: b.HeldOutEvent}, nil
		}
	}
	return CaseRef{}, fmt.Errorf("demorun: opportunity %s is not ranked in the demo-cases report (date %s); pass --opportunity with a ranked case", opportunityID, rep.Date)
}

// FindReport returns the explicit path, else the newest bench/reports/demo-cases-<date>.json under root.
func FindReport(explicit string, roots ...string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	var all []string
	for _, root := range roots {
		m, _ := filepath.Glob(filepath.Join(root, "bench", "reports", "demo-cases-*.json"))
		all = append(all, m...)
	}
	if len(all) == 0 {
		return "", fmt.Errorf("demorun: no bench/reports/demo-cases-*.json found under %s; pass --report", strings.Join(roots, " or "))
	}
	sort.Slice(all, func(i, j int) bool { return filepath.Base(all[i]) > filepath.Base(all[j]) })
	return all[0], nil
}

// ResolveSnapshot finds the CRMArena snapshot directory: the flag, then the candidates in order. A directory is a
// snapshot when it holds Opportunity.json. The manifest hash is compared with the report's: a difference is
// returned as a note (a re-export legitimately changes it because the manifest holds the export time), and the
// freeze's own check that the replay still reproduces the report is the real gate.
func ResolveSnapshot(flag string, candidates []string, wantManifestSHA string) (dir, note string, err error) {
	var tried []string
	list := candidates
	if flag != "" {
		list = append([]string{flag}, candidates...)
	}
	for _, c := range list {
		if c == "" {
			continue
		}
		tried = append(tried, c)
		if _, e := os.Stat(filepath.Join(c, "Opportunity.json")); e != nil {
			continue
		}
		got, hashErr := fileSHA256(filepath.Join(c, "manifest.json"))
		switch {
		case wantManifestSHA == "":
		case hashErr != nil:
			note = fmt.Sprintf("snapshot %s has no readable manifest.json, so it cannot be matched to the report", c)
		case got != wantManifestSHA:
			note = fmt.Sprintf("snapshot manifest hash %s differs from the report's %s (a re-export differs; the freeze still verifies that the replay reproduces the report)", got[:12], shortHash(wantManifestSHA))
		}
		return c, note, nil
	}
	return "", "", fmt.Errorf("demorun: no CRMArena snapshot found (searched: %s). The data is git-ignored and lives in the main checkout: "+
		"set GHOST_DEMO_DATA (or --data) to a directory holding Opportunity.json, or create it with `python bench/data/crmarena_export.py`", strings.Join(tried, "; "))
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// CreatedAt derives the manifest's created_at from the report date, never from the wall clock: the manifest hash
// covers it, and two freezes of the same case must agree.
func CreatedAt(reportDate string) (string, error) {
	d, err := time.Parse("2006-01-02", reportDate)
	if err != nil {
		return "", fmt.Errorf("demorun: report date %q is not YYYY-MM-DD: %w", reportDate, err)
	}
	return d.UTC().Format(time.RFC3339), nil
}
