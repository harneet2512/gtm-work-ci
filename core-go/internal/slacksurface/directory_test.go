package slacksurface

import (
	"context"
	"strings"
	"testing"
)

// HAR-124 (the record's Edit modal refused "a person not on this account"): the directory is the account graph's Person nodes (core
// types them "Person", and the graph is bounded) plus the state's buying group; and whatever display prints always saves back.

func TestCoreHTTPPeopleReadsTheGraphsPersonNodesAndTheStatesBuyingGroup(t *testing.T) {
	core := newMemCore()
	core.stateOnly = map[string]bool{fixturePriya: true} // in the buying group, not in the (bounded) graph
	dir, err := NewCoreHTTP(httpCore(t, core).URL, testToken, nil).People(context.Background(), "acct")
	if err != nil {
		t.Fatal(err)
	}
	if dir[fixtureMarco].Name != "Marco" || dir[fixtureMarco].Email != "marco@acme.example.test" {
		t.Fatalf("a Person node of the graph must be read (core types it \"Person\"): %+v", dir[fixtureMarco])
	}
	if got := dir[fixturePriya]; got.Name != "Priya" {
		t.Fatalf("a buying-group member the graph does not return must be in the directory: %+v", got)
	}
}

func TestEveryDirectoryEntryRoundTripsThroughDisplayAndParse(t *testing.T) {
	dir := Directory{
		"11111111-0000-4000-8000-000000000001": {ID: "11111111-0000-4000-8000-000000000001", Name: "Marco", Email: "marco@acme.example.test"},
		"11111111-0000-4000-8000-000000000002": {ID: "11111111-0000-4000-8000-000000000002", Name: "Name Only"},
		"11111111-0000-4000-8000-000000000003": {ID: "11111111-0000-4000-8000-000000000003", Email: "email.only@acme.example.test"},
		"11111111-0000-4000-8000-000000000004": {ID: "11111111-0000-4000-8000-000000000004", Name: "Sam Seller", Email: "sam@vendor.example.test"},
	}
	for id := range dir {
		r := Recipient{PersonID: id, Role: "to"}
		got, err := parseRecipients(dir.display(r), "to", inputTo, dir, []Recipient{r})
		if err != nil || len(got) != 1 || got[0].PersonID != id {
			t.Errorf("%s: display %q parsed to %+v, %v", id, dir.display(r), got, err)
		}
	}
}

func TestAnUnresolvedRecipientIsShownAsPlainWordsAndSavesBackUnchanged(t *testing.T) {
	unknownA, unknownB := "39c426ed-0000-4000-8000-00000000000a", "7d1e9b00-0000-4000-8000-00000000000b"
	existing := []Recipient{{PersonID: unknownA, Role: "to", Why: "asked"}, {PersonID: unknownB, Role: "to"}}
	dir := Directory{}
	line := joinAddrs(dir.displayAll(existing))
	if line != unresolvedLabel+", "+unresolvedLabel || strings.Contains(line, "39c426ed") {
		t.Fatalf("an unresolved recipient reads as plain words, never a raw id: %q", line)
	}
	got, err := parseRecipients(line, "to", inputTo, dir, existing)
	if err != nil || len(got) != 2 || got[0].PersonID != unknownA || got[0].Why != "asked" || got[1].PersonID != unknownB {
		t.Fatalf("an unchanged line must be accepted as the same recipients: %+v, %v", got, err)
	}
	if _, err := parseRecipients(unresolvedLabel, "to", inputTo, dir, nil); err == nil {
		t.Fatal("the placeholder with no unresolved recipient to stand for is free text and is refused")
	}
	if _, err := parseRecipients(unresolvedLabel+", "+unresolvedLabel+", "+unresolvedLabel, "to", inputTo, dir, existing); err == nil {
		t.Fatal("more placeholders than unresolved recipients is refused")
	}
}

// Select B, Edit, Save with To left as it was, for a recipient who is in the people but not in the directory the modal was built from.
func TestSelectEditSaveWithAnUnchangedToSucceedsForAnUnresolvedRecipient(t *testing.T) {
	h, core, _ := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	c, _ := core.fx.Strategies.Candidate(fixtureCandB)
	if len(c.To) == 0 {
		t.Fatal("fixture candidate B has no recipient")
	}
	delete(core.fx.Directory, c.To[0].PersonID) // the directory no longer knows the person candidate B addresses
	vals := editValues(core)
	if !strings.Contains(vals[inputTo], unresolvedLabel) {
		t.Fatalf("the modal shows the unresolved recipient as the placeholder, got %q", vals[inputTo])
	}
	vals[inputBody] += " (edited)"
	ack := runWork(t, h, viewCB("V1", CallbackEditModal, metaB, vals))
	if p := mustJSON(ack); strings.Contains(p, "errors") || strings.Contains(p, "No person on this account matches") {
		t.Fatalf("saving an unchanged To must be accepted, got %s", p)
	}
	if len(core.writes()) < 2 {
		t.Fatalf("the edit must reach core (choose + edit), writes=%v", core.writes())
	}
}

// The live Slack handler loads its directory with CoreHTTP.People, the same function the tests above exercise.
func TestTheHandlerLoadsItsDirectoryThroughCoreHTTPPeople(t *testing.T) {
	core := newMemCore()
	core.stateOnly = map[string]bool{fixturePriya: true}
	h := NewHandler(NewCoreHTTP(httpCore(t, core).URL, testToken, nil), &fakePoster{}, "C0TEST", "", quietLog())
	_, dir, err := h.load(context.Background(), FixtureRunID)
	if err != nil {
		t.Fatal(err)
	}
	if dir[fixtureMarco].Name == "" || dir[fixturePriya].Name == "" {
		t.Fatalf("the handler's directory must hold graph and buying-group people alike: %+v", dir)
	}
}
