package biwriter_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/biwriter"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

func TestBuildRefusesWhatItCannotCite(t *testing.T) {
	cases := map[string]func(*biwriter.Facts){
		"no trigger activity":       func(f *biwriter.Facts) { f.Activity.ID = "" },
		"no state diff":             func(f *biwriter.Facts) { f.Diff.ID = "" },
		"no projected graph diff":   func(f *biwriter.Facts) { f.Graph.JobIDs = nil },
		"no graph event id":         func(f *biwriter.Facts) { f.Graph.SourceEventID = "" },
		"no account":                func(f *biwriter.Facts) { f.AccountID = "" },
		"no held-out event":         func(f *biwriter.Facts) { f.HeldOutEventID = "" },
		"unmapped material field":   func(f *biwriter.Facts) { f.Diff.Entries = []biwriter.DiffEntry{entry("mood", "set", nil, "good")} },
		"first version has no diff": func(f *biwriter.Facts) { f.Diff.ToVersion = f.Diff.FromVersion },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := facts()
			mutate(&f)
			if _, err := biwriter.Build(f, ids, now); err == nil {
				t.Fatal("Build must fail loudly instead of writing an uncited or mislabelled update")
			}
		})
	}
	if _, err := biwriter.Build(facts(), biwriter.IDs{}, now); err == nil {
		t.Fatal("Build needs the ids of the objects it creates")
	}
}

func TestEveryReducerFieldAndStructureHasADimensionAndALabel(t *testing.T) {
	for _, field := range biwriter.KnownFields() {
		if biwriter.DimensionOf(field) == "" {
			t.Errorf("field %q has no change dimension", field)
		}
	}
	for _, field := range reducer.FieldNames() {
		if biwriter.DimensionOf(field) == "" {
			t.Errorf("state field %q has no change dimension: the diff would make Build fail", field)
		}
	}
}

func TestLongValuesAreTruncatedToTheContractLimits(t *testing.T) {
	f := facts()
	huge := strings.Repeat("x", 5000)
	f.Diff.Entries = []biwriter.DiffEntry{
		entry("blockers", "changed", items(huge), items(huge, huge, huge), ev(actN, "q")),
		entry("stage", "changed", huge, huge, ev(actN, "q")),
	}
	r := build(t, f)
	for _, c := range r.BI.Claims {
		if n := len([]rune(c.Statement)); n > 500 || n == 0 {
			t.Errorf("statement length %d outside 1..500", n)
		}
	}
	if n := len([]rune(r.BI.Summary)); n > 600 {
		t.Errorf("summary length %d > 600", n)
	}
	if n := len([]rune(r.BI.WhyItMatters)); n > 1500 {
		t.Errorf("why_it_matters length %d > 1500", n)
	}
}

func TestDocumentsConformToTheContracts(t *testing.T) {
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	withTransition := facts()
	withTransition.Transition = &biwriter.Transition{ID: "0e5a0000-0000-4000-8000-000000000002", Status: "CANDIDATE", FromState: "unknown", ToState: ptr("EXPANSION"),
		Missing: []biwriter.Fact{{Key: "owner_stabilized", Description: "d", Required: true}}}
	nonMaterial := facts()
	nonMaterial.Diff.IsMaterial = false
	nonMaterial.Diff.Entries = []biwriter.DiffEntry{{Field: "summary", Op: "changed", Before: "a", After: "b"}}
	for name, f := range map[string]biwriter.Facts{"no transition": facts(), "candidate": withTransition, "non material": nonMaterial} {
		t.Run(name, func(t *testing.T) {
			r := build(t, f)
			doc, _ := json.Marshal(r.Change)
			if err := v.Validate("account_change", doc); err != nil {
				t.Fatalf("account_change: %v\n%s", err, doc)
			}
			if r.BI == nil {
				return
			}
			doc, _ = json.Marshal(r.BI)
			if err := v.Validate("business_intelligence_update", doc); err != nil {
				t.Fatalf("business_intelligence_update: %v\n%s", err, doc)
			}
		})
	}
}

func TestValidateCatchesWhatBuildMustNeverProduce(t *testing.T) {
	f := facts()
	base := build(t, f)
	clone := func() biwriter.Result {
		raw, _ := json.Marshal(base)
		var r biwriter.Result
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	cases := map[string]func(*biwriter.Result){
		"an uncited claim": func(r *biwriter.Result) { r.BI.Claims[1].EvidenceRefs = nil },
		"a claim citing an unknown task": func(r *biwriter.Result) {
			r.BI.Claims[0].EvidenceRefs[0].ActivityID = "0ac70000-0000-4000-8000-0000000000ff"
		},
		"a claim naming no diff entry":   func(r *biwriter.Result) { r.BI.Claims[0].StateDiffField = nil },
		"a claim naming a foreign entry": func(r *biwriter.Result) { r.BI.Claims[0].StateDiffField = ptr("stage") },
		"a graph item that is not in diff": func(r *biwriter.Result) {
			r.BI.Claims[0].GraphDiffItems = []biwriter.GraphDiffItem{{Kind: "node", Type: "Person", ID: "ghost"}}
		},
		"an update of another change":     func(r *biwriter.Result) { r.BI.AccountChangeID = "0acc0000-0000-4000-8000-0000000000ff" },
		"a material change without proof": func(r *biwriter.Result) { r.Change.EvidenceRefs = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := clone()
			mutate(&r)
			if err := biwriter.Validate(f, r); err == nil {
				t.Fatal("Validate accepted it")
			}
		})
	}
	if err := biwriter.Validate(f, clone()); err != nil {
		t.Fatalf("an untouched result must validate: %v", err)
	}
}
