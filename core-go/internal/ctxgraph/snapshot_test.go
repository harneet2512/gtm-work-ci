package ctxgraph

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func buildAcme(t *testing.T, s ids) Snapshot {
	t.Helper()
	snap, err := BuildSnapshot(context.Background(), pg.DB, s["A"])
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return snap
}

func countLabel(snap Snapshot, label string) int { return len(keysOfType(snap, "node", label)) }
func countRel(snap Snapshot, typ string) int     { return len(keysOfType(snap, "edge", typ)) }

func TestSnapshotProjectsTheCanonicalAccountSubgraph(t *testing.T) {
	s := seedWorld(t)
	snap := buildAcme(t, s)

	wantNodes := map[string]int{
		LabelAccount: 1, LabelOpportunity: 1, LabelPerson: 3, LabelConversation: 3, LabelActivity: 2, LabelDocument: 1,
		LabelClaim: 5, LabelCommitment: 1, LabelSignal: 1, LabelDecisionEpisode: 1, LabelKnowledge: 1,
	}
	for label, want := range wantNodes {
		if got := countLabel(snap, label); got != want {
			t.Errorf("%s nodes = %d, want %d", label, got, want)
		}
	}
	wantEdges := map[string]int{
		RelWorksAt: 1, RelBelongsTo: 1, RelChampionFor: 2, RelEconomicBuyerFor: 1, RelParticipatedIn: 1, RelInvolves: 2,
		RelAbout: 2, RelSharedWith: 1, RelOwns: 1, RelAboutAccount: 8, RelAboutPerson: 3, RelAboutOpportunity: 7,
		RelSupportedBy: 7, RelMadeBy: 1, RelMadeIn: 1, RelDerivedFrom: 2, RelTriggeredBy: 2, RelUsedKnowledge: 1, RelAppliesTo: 4,
	}
	for typ, want := range wantEdges {
		if got := countRel(snap, typ); got != want {
			t.Errorf("%s edges = %d, want %d", typ, got, want)
		}
	}
	if snap.Skipped[skipRelNotInOntology] != 1 || snap.Skipped[skipEndpointNotAllowed] != 0 {
		t.Errorf("skipped = %v, want only the rel_type_not_in_ontology (blocks): a person in a non-conversation activity is an INVOLVES edge", snap.Skipped)
	}
}

func TestSnapshotIsDeterministic(t *testing.T) {
	s := seedWorld(t)
	a, b := buildAcme(t, s), buildAcme(t, s)
	if !reflect.DeepEqual(a.Hashes(), b.Hashes()) || Digest(a.Hashes()) != Digest(b.Hashes()) {
		t.Fatal("two snapshots of the same Postgres state differ")
	}
}

func TestSnapshotKeepsHistoryAndProvenance(t *testing.T) {
	s := seedWorld(t)
	snap := buildAcme(t, s)
	var closed, open int
	for _, e := range snap.Edges {
		if e.Type != RelChampionFor {
			continue
		}
		if _, hasTo := e.Props["valid_to"]; hasTo {
			closed++
			if e.Props["status"] != "closed" {
				t.Errorf("closed edge status = %v", e.Props["status"])
			}
		} else {
			open++
		}
	}
	if closed != 1 || open != 1 {
		t.Fatalf("champion_for edges: %d closed, %d open; history must keep both", closed, open)
	}
	old := nodeByID(t, snap, s["C_OLD"])
	if old.Props["status"] != "superseded" || old.Props["valid_to"] == nil {
		t.Errorf("superseded claim lost its history: %v", old.Props)
	}
	call := nodeByID(t, snap, s["C_CHAMP"])
	if got := call.Props["evidence_activity_ids"].([]string); len(got) != 1 || got[0] != s["ACT_CALL"] {
		t.Errorf("claim evidence activity = %v", got)
	}
	if got := call.Props["source_event_ids"].([]string); len(got) != 1 || got[0] != s["SE_CALL"] {
		t.Errorf("claim source event = %v, want the call's source event", got)
	}
}

func nodeByID(t *testing.T, snap Snapshot, id string) Node {
	t.Helper()
	for _, n := range snap.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("node %s not in the snapshot", id)
	return Node{}
}

func TestSnapshotScopesToTheAccount(t *testing.T) {
	s := seedWorld(t)
	snap := buildAcme(t, s)
	for _, n := range snap.Nodes {
		if n.ID == s["B"] || n.ID == s["OPPB"] || n.ID == s["BCONTACT"] || n.ID == s["ACT_B"] {
			t.Errorf("%s %s of another account leaked into Acme's snapshot", n.Primary(), n.ID)
		}
		if n.Scope != "" && n.Scope != s["A"] {
			t.Errorf("%s scoped to %s", n.ID, n.Scope)
		}
	}
	rep := nodeByID(t, snap, s["REP"])
	if rep.Scope != "" {
		t.Error("an employee is a global node (no account_id)")
	}
}

func TestSnapshotCarriesVisibilityForTheReadPath(t *testing.T) {
	s := seedWorld(t)
	snap := buildAcme(t, s)
	if v := nodeByID(t, snap, s["ACT_HID"]).Props["visibility"]; v != "person" {
		t.Errorf("hidden activity visibility = %v", v)
	}
	if v := nodeByID(t, snap, s["ACT_MAIL"]).Props["visibility"]; v != "org" {
		t.Errorf("org activity visibility = %v", v)
	}
}

func TestSnapshotOfUnknownAccountIsAnError(t *testing.T) {
	seedWorld(t)
	_, err := BuildSnapshot(context.Background(), pg.DB, "00000000-0000-4000-8000-000000000000")
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestEverySnapshotElementConformsToTheOntology(t *testing.T) {
	s := seedWorld(t)
	for _, acct := range []string{s["A"], s["B"]} {
		snap, err := BuildSnapshot(context.Background(), pg.DB, acct)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateSnapshot(snap); err != nil {
			t.Errorf("account %s: %v", acct, err)
		}
	}
}
