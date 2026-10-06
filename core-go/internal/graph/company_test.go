package graph_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/graph"
)

const orgFile = "../../../fixtures/world/org.json"

func smallOrg() graph.Company {
	mgr := "person:rachel_ortiz"
	return graph.Company{
		Organization: graph.CompanyInfo{Name: "Vendor Co.", Domain: "vendor.example"},
		People: []graph.CompanyPerson{
			{Key: "person:dana_kim", Kind: "employee", DisplayName: "Dana Kim", Email: "Dana@vendor.example", Title: "AE", SlackUser: "U02DANA", ManagerKey: &mgr},
			{Key: "person:rachel_ortiz", Kind: "employee", DisplayName: "Rachel Ortiz", Email: "rachel@vendor.example", Title: "Director", SlackUser: "U02RACHEL"},
		},
	}
}

func TestLoadOrgReadsTheFixture(t *testing.T) {
	org, err := graph.LoadCompany(orgFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(org.People) != 3 || org.Organization.Domain != "vendor.example" {
		t.Errorf("unexpected org %+v", org)
	}
	if _, err := graph.LoadCompany(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("a missing file must fail")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{"people": [{"unknown_field": 1}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := graph.LoadCompany(bad); err == nil {
		t.Error("unknown fields must be rejected")
	}
}

func TestSeedOrgCreatesEmployeesMappingsAndReportingLines(t *testing.T) {
	resetDB(t)

	rep, err := graph.SeedCompany(ctx, env.DB, smallOrg(), t0)
	if err != nil {
		t.Fatal(err)
	}

	if rep.Created != 2 {
		t.Errorf("created %d employees, want 2", rep.Created)
	}
	dana := str(t, `SELECT entity_id::text FROM entity_source_mappings WHERE source_system='email' AND source_key='dana@vendor.example' AND valid_to IS NULL`)
	if dana == "" {
		t.Fatal("email mapping (lower-cased) missing")
	}
	if got := str(t, `SELECT kind || '/' || coalesce(account_id::text,'none') || '/' || title FROM people WHERE id = $1::uuid`, dana); got != "employee/none/AE" {
		t.Errorf("person row = %s", got)
	}
	if got := str(t, `SELECT entity_id::text FROM entity_source_mappings WHERE source_system='slack' AND source_key='U02DANA'`); got != dana {
		t.Errorf("slack mapping points at %s, want Dana", got)
	}
	if got := str(t, `SELECT method FROM entity_source_mappings WHERE source_system='email' AND source_key='dana@vendor.example'`); got != "seed" {
		t.Errorf("method = %s, want seed", got)
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE rel_type='reports_to' AND src_id = $1::uuid AND valid_to IS NULL`, dana); got != 1 {
		t.Errorf("Dana has %d open reports_to edges, want 1", got)
	}
}

func TestSeedOrgIsIdempotent(t *testing.T) {
	resetDB(t)
	if _, err := graph.SeedCompany(ctx, env.DB, smallOrg(), t0); err != nil {
		t.Fatal(err)
	}
	snapshot := func() [3]int {
		return [3]int{num(t, `SELECT count(*) FROM people`), num(t, `SELECT count(*) FROM entity_source_mappings`), num(t, `SELECT count(*) FROM relationships`)}
	}
	before := snapshot()

	rep, err := graph.SeedCompany(ctx, env.DB, smallOrg(), t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if after := snapshot(); after != before {
		t.Errorf("row counts changed on re-seed: %v -> %v", before, after)
	}
	if rep.Created != 0 || rep.Updated != 0 {
		t.Errorf("re-seed report = %+v, want nothing created or updated", rep)
	}
}

func TestSeedOrgAppliesChangesWithHistory(t *testing.T) {
	resetDB(t)
	if _, err := graph.SeedCompany(ctx, env.DB, smallOrg(), t0); err != nil {
		t.Fatal(err)
	}
	org := smallOrg()
	org.People[0].SlackUser = "U99NEW"
	org.People[0].Title = "Senior AE"
	org.People[0].ManagerKey = nil

	rep, err := graph.SeedCompany(ctx, env.DB, org, t0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if rep.Updated != 1 {
		t.Errorf("updated = %d, want 1 (Dana)", rep.Updated)
	}
	if got := num(t, `SELECT count(*) FROM entity_source_mappings WHERE source_system='slack' AND source_key='U02DANA' AND valid_to IS NOT NULL`); got != 1 {
		t.Errorf("the old slack mapping must be closed (found %d)", got)
	}
	if got := str(t, `SELECT title FROM people WHERE primary_email='dana@vendor.example'`); got != "Senior AE" {
		t.Errorf("title = %s", got)
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE rel_type='reports_to' AND valid_to IS NULL`); got != 0 {
		t.Errorf("Dana's reports_to edge must be closed when she has no manager (open=%d)", got)
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE rel_type='reports_to' AND valid_to IS NOT NULL`); got != 1 {
		t.Errorf("closed reports_to edges = %d, want 1 (history kept)", got)
	}
}

func TestSeedOrgRejectsBadInputWithoutWritingAnything(t *testing.T) {
	unknownMgr := "person:nobody"
	cases := map[string]func(*graph.Company){
		"email outside our domain": func(o *graph.Company) { o.People[0].Email = "dana@acme.com" },
		"contact kind":             func(o *graph.Company) { o.People[0].Kind = "contact" },
		"no email":                 func(o *graph.Company) { o.People[0].Email = "" },
		"no name":                  func(o *graph.Company) { o.People[0].DisplayName = " " },
		"unknown manager":          func(o *graph.Company) { o.People[0].ManagerKey = &unknownMgr },
		"duplicate email":          func(o *graph.Company) { o.People[1].Email = o.People[0].Email },
		"duplicate key":            func(o *graph.Company) { o.People[1].Key = o.People[0].Key },
		"self manager":             func(o *graph.Company) { k := o.People[0].Key; o.People[0].ManagerKey = &k },
		"no people":                func(o *graph.Company) { o.People = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			resetDB(t)
			org := smallOrg()
			mutate(&org)
			_, err := graph.SeedCompany(ctx, env.DB, org, t0)
			if err == nil {
				t.Fatal("invalid org accepted")
			}
			if n := num(t, `SELECT count(*) FROM people`); n != 0 {
				t.Errorf("%d people written by a failed seed (error: %v)", n, err)
			}
		})
	}
}
