package demomine

import (
	"testing"
)

func sampleManifest(account, opp, diff string) Manifest {
	o := opp
	d := diff
	ev := ManifestEvent{Event: HeldOutEvent{EventID: "e1", OccurredAt: "2025-01-01T00:00:00Z", ReplayPosition: 1},
		StateAfter: StateRef{AccountID: account, OpportunityID: &o, Version: 2}, StateDiffID: &d,
		IsMaterial: true, MaterialDimensions: []string{DimIntent}}
	return Manifest{ID: "m", AccountID: account, OpportunityID: opp, DataCutoff: "2025-01-01T00:00:00Z",
		Events: []ManifestEvent{ev}, HeldOutEvent: HeldOutEvent{EventID: "e2", ReplayPosition: 2},
		WhySelected: "why", CreatedAt: "2026-10-03T12:00:00Z"}
}

func TestSealHashIgnoresDatabaseAssignedIDs(t *testing.T) {
	a := sampleManifest("aaaaaaaa-0000-4000-8000-000000000001", "bbbbbbbb-0000-4000-8000-000000000001", "cccccccc-0000-4000-8000-000000000001")
	b := sampleManifest("aaaaaaaa-0000-4000-8000-000000000002", "bbbbbbbb-0000-4000-8000-000000000002", "cccccccc-0000-4000-8000-000000000002")
	ja, err := seal(&a)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := seal(&b)
	if err != nil {
		t.Fatal(err)
	}
	if a.ContentSHA256 != b.ContentSHA256 {
		t.Fatalf("the hash depends on database-assigned ids: %s vs %s", a.ContentSHA256, b.ContentSHA256)
	}
	for _, doc := range [][]byte{ja, jb} {
		if err := VerifyHash(doc); err != nil {
			t.Fatalf("a sealed document must verify: %v", err)
		}
	}
}

func TestSealHashCoversTheContent(t *testing.T) {
	base := sampleManifest("a", "b", "c")
	if _, err := seal(&base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Manifest){
		"created_at":  func(m *Manifest) { m.CreatedAt = "2026-10-04T12:00:00Z" },
		"why":         func(m *Manifest) { m.WhySelected = "other" },
		"dimension":   func(m *Manifest) { m.Events[0].MaterialDimensions = []string{DimRisk} },
		"held-out id": func(m *Manifest) { m.HeldOutEvent.EventID = "e3" },
		"version":     func(m *Manifest) { m.Events[0].StateAfter.Version = 3 },
	} {
		m := sampleManifest("a", "b", "c")
		mutate(&m)
		if _, err := seal(&m); err != nil {
			t.Fatal(err)
		}
		if m.ContentSHA256 == base.ContentSHA256 {
			t.Errorf("changing %s did not change the hash", name)
		}
	}
}

func TestManifestIDDoesNotDependOnTheDatabase(t *testing.T) {
	o := FreezeOptions{OpportunityID: "006SF1", HeldOutID: "e2"}
	if manifestID(o, "e2") != manifestID(o, "e2") || manifestID(o, "e2") == manifestID(FreezeOptions{OpportunityID: "006SF2"}, "e2") {
		t.Fatal("the manifest id must be a function of the Salesforce opportunity and the held-out event")
	}
}
