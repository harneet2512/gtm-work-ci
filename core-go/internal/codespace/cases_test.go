package codespace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

func TestDefaultCasesAreMedTechThenEcoLiteEachInItsOwnDatabase(t *testing.T) {
	cases := DefaultCases()
	if len(cases) != 2 {
		t.Fatalf("cases = %d, want 2", len(cases))
	}
	if cases[0].Slot != SlotCase1 || cases[0].OpportunityID != "006Wt000007BHzBIAW" || cases[0].Label != "MedTech Advances" {
		t.Errorf("case 1 = %+v, want MedTech Advances 006Wt000007BHzBIAW", cases[0])
	}
	if cases[1].Slot != SlotCase2 || cases[1].OpportunityID != "006Wt000007BDAnIAO" || cases[1].Label != "EcoLite Innovations" {
		t.Errorf("case 2 = %+v, want EcoLite Innovations 006Wt000007BDAnIAO", cases[1])
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", c.Slot, err)
		}
		for _, db := range []string{c.Database, c.Template()} {
			if seen[db] || db == "ghost_demo" {
				t.Errorf("database %s is shared or is the host's own", db)
			}
			seen[db] = true
		}
	}
	if cases[0].Template() != "ghost_case1_frozen" {
		t.Errorf("template = %s", cases[0].Template())
	}
}

func TestValidateRejectsNamesThatCouldEscapeAPathOrQuoteSQL(t *testing.T) {
	good := DefaultCases()[0]
	for name, mutate := range map[string]func(*Case){
		"slot with a dot":      func(c *Case) { c.Slot = "../x" },
		"empty slot":           func(c *Case) { c.Slot = "" },
		"uppercase database":   func(c *Case) { c.Database = "Ghost" },
		"quote in database":    func(c *Case) { c.Database = `g"; drop database x; --` },
		"database too long":    func(c *Case) { c.Database = strings.Repeat("a", 60) },
		"not an opportunity":   func(c *Case) { c.OpportunityID = "001Wt00000PHVyfIAH" },
		"opportunity with sql": func(c *Case) { c.OpportunityID = "006Wt000007BHzBIAW'; --" },
	} {
		c := good
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: Validate accepted %+v", name, c)
		}
	}
}

func TestFindCaseNamesTheKnownSlotsOnAMiss(t *testing.T) {
	if c, err := FindCase(DefaultCases(), SlotCase2); err != nil || c.Label != "EcoLite Innovations" {
		t.Fatalf("FindCase = %+v, %v", c, err)
	}
	_, err := FindCase(DefaultCases(), "case9")
	if err == nil || !strings.Contains(err.Error(), "case1, case2") {
		t.Fatalf("err = %v, want the known slots listed", err)
	}
}

func TestPathsKeepEachCaseUnderItsOwnDirectory(t *testing.T) {
	l := demorun.NewLayout(filepath.Join("repo"))
	l.StateDir = filepath.Join("state")
	p := Paths{Layout: l}
	if got := p.Manifest("case2"); got != filepath.Join("state", "cases", "case2", "manifest.json") {
		t.Errorf("manifest = %s", got)
	}
	if p.State("case1") == p.State("case2") || p.Manifest("case1") == p.Manifest("case2") {
		t.Error("cases must not share files")
	}
	if p.ActiveFile() != filepath.Join("state", "active-case") || p.GraphMarker("case2") != filepath.Join("state", "graph-case2") {
		t.Errorf("markers = %s %s", p.ActiveFile(), p.GraphMarker("case2"))
	}
}

func TestMarkersRoundTripAndAMissingOneIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "active-case")
	if got, err := ReadMarker(path); err != nil || got != "" {
		t.Fatalf("missing marker = %q, %v", got, err)
	}
	if err := WriteMarker(path, "case2"); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadMarker(path); err != nil || got != "case2" {
		t.Fatalf("marker = %q, %v", got, err)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("the temp file must be renamed away")
	}
	dir := t.TempDir() // a directory where a file is expected is a read error, not an empty marker
	if _, err := ReadMarker(dir); err == nil {
		t.Error("reading a directory must fail")
	}
	if err := WriteMarker(filepath.Join(dir, "x"), "a"); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarker(filepath.Join(dir, "x", "y", "z"), "a"); err == nil {
		t.Error("writing below a file must fail")
	}
}

// TestDemoCaseLabelsAreTheAccountNamesOfTheMinedReport ties both labels to bench/reports/demo-cases-2026-10-04.json, the
// source of truth for who the demo's accounts are (an earlier build labelled case 2 "EcoVision Engineering").
func TestDemoCaseLabelsAreTheAccountNamesOfTheMinedReport(t *testing.T) {
	dir, _ := os.Getwd()
	var raw []byte
	for {
		b, err := os.ReadFile(filepath.Join(dir, "bench", "reports", "demo-cases-2026-10-04.json"))
		if err == nil {
			raw = b
			break
		}
		if filepath.Dir(dir) == dir {
			t.Skip("the mined report is not in this checkout (the public CI mirror strips bench/)")
		}
		dir = filepath.Dir(dir)
	}
	var report struct {
		TopCases []struct {
			Rank            int    `json:"rank"`
			AccountName     string `json:"account_name"`
			OpportunityID   string `json:"opportunity_id"`
			OpportunityName string `json:"opportunity_name"`
		} `json:"top_cases"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	for _, c := range DefaultCases() {
		found := false
		for _, r := range report.TopCases {
			if r.OpportunityID == c.OpportunityID {
				found = true
				if r.AccountName != c.Label {
					t.Errorf("%s: label %q, but the report names the account %q", c.Slot, c.Label, r.AccountName)
				}
			}
		}
		if !found {
			t.Errorf("%s: opportunity %s is not among the report's top cases", c.Slot, c.OpportunityID)
		}
	}
}

func TestCasesFromEnvKeepsTheLabelUnlessAnotherCaseIsSelected(t *testing.T) {
	same := CasesFromEnv(map[string]string{"GHOST_DEMO_CASE2_OPPORTUNITY": EcoLiteOpportunity})
	if same[1].Label != "EcoLite Innovations" {
		t.Errorf("naming the default case must not relabel it: %q", same[1].Label)
	}
	other := CasesFromEnv(map[string]string{"GHOST_DEMO_CASE2_OPPORTUNITY": "006Wt000007BC0DIAW"})
	if other[1].OpportunityID != "006Wt000007BC0DIAW" || !strings.Contains(other[1].Label, "006Wt000007BC0DIAW") {
		t.Errorf("another ranked case is selected by id: %+v", other[1])
	}
	if got := CasesFromEnv(nil); got[1].Label != "EcoLite Innovations" || got[0].Label != "MedTech Advances" {
		t.Errorf("defaults = %+v", got)
	}
}
