package demorun

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demomine"
)

func sampleReport() demomine.Report {
	return demomine.Report{
		Schema: "demo-cases.v1", Date: "2026-10-04",
		Snapshot: demomine.SnapshotInfo{Name: "crmarena_b2b", ManifestSHA256: "abc"},
		Top: []demomine.Case{
			{Rank: 1, AccountName: "MedTech Advances", OpportunityID: MedTechOpportunity, OpportunityName: "Advanced Data Protection",
				HeldOut: demomine.EventRecord{EventID: "1e32b30c-3892-5e61-9dd5-411dc5392ac5"}},
			{Rank: 2, AccountName: "Pioneer Envisions", OpportunityID: "006Wt000007BF69IAG", HeldOut: demomine.EventRecord{EventID: "76dcdec2-0000-5000-8000-000000000000"}},
		},
		Ranking: []demomine.RankedBrief{{Rank: 11, OpportunityID: "006OTHER", HeldOutEvent: "ffffffff-0000-5000-8000-000000000000"}},
	}
}

func TestPickCaseFindsTheTopCaseByOpportunity(t *testing.T) {
	c, err := PickCase(sampleReport(), MedTechOpportunity)
	if err != nil || c.HeldOutEventID != "1e32b30c-3892-5e61-9dd5-411dc5392ac5" || c.AccountName != "MedTech Advances" || c.Rank != 1 {
		t.Fatalf("PickCase = %+v, %v", c, err)
	}
}

func TestPickCaseFallsBackToTheFullRankingAndRefusesUnknowns(t *testing.T) {
	c, err := PickCase(sampleReport(), "006OTHER")
	if err != nil || c.HeldOutEventID != "ffffffff-0000-5000-8000-000000000000" || c.Rank != 11 {
		t.Fatalf("ranking fallback = %+v, %v", c, err)
	}
	if _, err := PickCase(sampleReport(), "006NOPE"); err == nil || !strings.Contains(err.Error(), "006NOPE") {
		t.Fatalf("an opportunity the report does not rank must be an error naming it, got %v", err)
	}
}

func TestPickCaseDefaultsToTheMedTechCase(t *testing.T) {
	if MedTechOpportunity != "006Wt000007BHzBIAW" {
		t.Fatalf("the default demo case is MedTech Advances (opportunity 006Wt000007BHzBIAW), got %s", MedTechOpportunity)
	}
}

func TestFindReportPicksTheNewestByDateInTheFileName(t *testing.T) {
	root := t.TempDir()
	reports := filepath.Join(root, "bench", "reports")
	if err := os.MkdirAll(reports, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"demo-cases-2026-10-03.json", "demo-cases-2026-10-04.json", "demo-cases-2026-10-04.md", "metrics-2026-10-09.json"} {
		if err := os.WriteFile(filepath.Join(reports, n), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := FindReport("", root)
	if err != nil || filepath.Base(got) != "demo-cases-2026-10-04.json" {
		t.Fatalf("FindReport = %q, %v", got, err)
	}
	if got, err := FindReport("/explicit/report.json", root); err != nil || got != "/explicit/report.json" {
		t.Fatalf("an explicit path wins: %q %v", got, err)
	}
	if _, err := FindReport("", t.TempDir()); err == nil {
		t.Fatal("no report anywhere must be an error")
	}
}

func writeSnapshot(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"counts":{}}`)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Opportunity.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(manifest)
	return hex.EncodeToString(sum[:])
}

func TestResolveSnapshotPrefersTheFlagThenCandidatesAndChecksTheHash(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	hashA := writeSnapshot(t, a)
	writeSnapshot(t, b)
	if err := os.WriteFile(filepath.Join(b, "manifest.json"), []byte(`{"counts":{"Account":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, note, err := ResolveSnapshot(b, []string{a}, hashA)
	if err != nil || dir != b {
		t.Fatalf("the flag must win: %q %v", dir, err)
	}
	if !strings.Contains(note, "differs") {
		t.Fatalf("a manifest hash that differs from the report's is a warning, got %q", note)
	}
	dir, note, err = ResolveSnapshot("", []string{filepath.Join(t.TempDir(), "missing"), a}, hashA)
	if err != nil || dir != a || strings.Contains(note, "differs") {
		t.Fatalf("first existing candidate with a matching hash: %q %q %v", dir, note, err)
	}
}

func TestResolveSnapshotErrorListsWhatWasSearchedAndHowToGetIt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "data", "crmarena_b2b")
	_, _, err := ResolveSnapshot("", []string{missing}, "abc")
	if err == nil {
		t.Fatal("no snapshot must be an error")
	}
	for _, want := range []string{missing, "crmarena_export.py", "GHOST_DEMO_DATA"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestResolveSnapshotIgnoresADirectoryWithoutOpportunityJSON(t *testing.T) {
	empty := t.TempDir()
	if _, _, err := ResolveSnapshot("", []string{empty}, ""); err == nil {
		t.Fatal("a directory with no Opportunity.json is not a snapshot")
	}
}

func TestCreatedAtIsDerivedFromTheReportDateNotTheWallClock(t *testing.T) {
	got, err := CreatedAt("2026-10-04")
	if err != nil || got != "2026-10-04T00:00:00Z" {
		t.Fatalf("CreatedAt = %q, %v", got, err)
	}
	if _, err := CreatedAt("yesterday"); err == nil {
		t.Fatal("a bad date must be an error")
	}
}
