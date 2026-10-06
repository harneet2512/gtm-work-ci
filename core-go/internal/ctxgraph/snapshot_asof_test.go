package ctxgraph

import (
	"context"
	"strings"
	"testing"
	"time"
)

var (
	farFuture = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	callAt    = time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC) // ACT_CALL, the trigger of the seeded decision episode
)

func buildAsOf(t *testing.T, s ids, at time.Time) Snapshot {
	t.Helper()
	snap, err := BuildSnapshotAsOf(context.Background(), pg.DB, s["A"], at)
	if err != nil {
		t.Fatalf("snapshot as of %s: %v", at, err)
	}
	return snap
}

func findNode(snap Snapshot, id string) (Node, bool) {
	for _, n := range snap.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

func edgeByType(snap Snapshot, typ string) []Edge {
	var out []Edge
	for _, e := range snap.Edges {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// With a cutoff after every event the world snapshot is the current one, byte for byte, except for what
// has no world time: knowledge, a person's title and display name, the opportunity owner, and the time and verdict of a decision episode.
func TestSnapshotAsOfAfterEveryEventIsTheCurrentSnapshotExceptWhatHasNoWorldTime(t *testing.T) {
	s := seedWorld(t)
	current, world := buildAcme(t, s).Hashes(), buildAsOf(t, s, farFuture).Hashes()
	noWorldTime := func(key string) bool {
		return strings.Contains(key, "|Knowledge|") || strings.Contains(key, "|DecisionEpisode|") ||
			strings.HasPrefix(key, "E|APPLIES_TO|") || strings.HasPrefix(key, "E|USED_KNOWLEDGE|") ||
			strings.HasPrefix(key, "N|Person|") || key == NodeKey(LabelOpportunity, s["OPP"]) ||
			strings.HasPrefix(key, "E|SUPPORTED_BY|ke:") || strings.Contains(key, "episode:")
	}
	for key, h := range current {
		if noWorldTime(key) {
			continue
		}
		if world[key] != h {
			t.Errorf("%s: current hash %.8s, world hash %.8s", key, h, world[key])
		}
	}
	for key := range world {
		if _, ok := current[key]; !ok {
			t.Errorf("%s exists only in the world snapshot", key)
		}
	}
	if len(keysOfType(buildAsOf(t, s, farFuture), "node", LabelKnowledge)) != 0 {
		t.Error("knowledge has no lifecycle history, so no world read carries it")
	}
}

func TestSnapshotAsOfDropsInPlaceAttributes(t *testing.T) {
	s := seedWorld(t)
	snap := buildAsOf(t, s, farFuture)
	champ, ok := findNode(snap, s["CHAMP"])
	if !ok {
		t.Fatal("the champion is missing")
	}
	if _, has := champ.Props["title"]; has {
		t.Error("a person's title is overwritten in place; no value of it exists at a past time")
	}
	if opp, _ := findNode(snap, s["OPP"]); opp.Props["owner_person_id"] != nil {
		t.Error("the opportunity owner is overwritten in place; no value of it exists at a past time")
	}
	if cur := buildAcme(t, s); func() bool { n, _ := findNode(cur, s["CHAMP"]); return n.Props["title"] == nil }() {
		t.Error("the current snapshot must keep the title")
	}
}

// A decision episode is placed at its latest trigger: absent at the trigger's own time, present just after,
// and carrying the trigger time instead of the wall-clock created_at.
func TestSnapshotAsOfPlacesAnEpisodeAtItsTrigger(t *testing.T) {
	s := seedWorld(t)
	if _, ok := findNode(buildAsOf(t, s, callAt), s["EP"]); ok {
		t.Fatal("the episode was triggered by the call, which is not before the call")
	}
	snap := buildAsOf(t, s, callAt.Add(time.Microsecond))
	ep, ok := findNode(snap, s["EP"])
	if !ok {
		t.Fatal("the episode must exist just after its trigger")
	}
	if ep.Props["valid_from"] != ts(callAt) || ep.Props["created_at"] != ts(callAt) {
		t.Errorf("episode time = %v / %v, want the trigger time %s", ep.Props["valid_from"], ep.Props["created_at"], ts(callAt))
	}
	if len(edgeByType(snap, RelUsedKnowledge)) != 0 {
		t.Error("no knowledge edge without knowledge")
	}
	if err := ValidateSnapshot(snap); err != nil {
		t.Fatal(err)
	}
}

// A claim a human rejected has no world time, so it is in no world snapshot.
func TestSnapshotAsOfLeavesOutRejectedClaims(t *testing.T) {
	s := seedWorld(t)
	runSeed(t, s, []seedStep{{"", `UPDATE claims SET status = 'rejected' WHERE id = $C_DOC`}})
	if _, ok := findNode(buildAsOf(t, s, farFuture), s["C_DOC"]); ok {
		t.Fatal("a rejected claim must not appear")
	}
	if _, ok := findNode(buildAcme(t, s), s["C_DOC"]); !ok {
		t.Fatal("the current snapshot keeps it")
	}
}

// M3: an entity nothing world-timed touches before T is not in the world at T (there is no timeless
// exception); the same entity is gone from a world read at any T when it has no evidence at all.
func TestSnapshotAsOfHidesPeopleAndDealsWithoutEvidenceBeforeT(t *testing.T) {
	s := seedWorld(t)
	runSeed(t, s, []seedStep{
		{"LONER", `INSERT INTO people (kind, display_name, account_id) VALUES ('contact','Lone Contact',$A) RETURNING id`},
		{"OPPX", `INSERT INTO opportunities (account_id, name, motion) VALUES ($A,'Created later','expansion') RETURNING id`},
	})
	early := buildAsOf(t, s, time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)) // before every relationship and activity of the seed
	for _, id := range []string{s["LONER"], s["OPPX"], s["BUYER"], s["OPP"]} {
		if _, ok := findNode(early, id); ok {
			t.Errorf("%s has no world-timed evidence before 08-31 and must not be there", id)
		}
	}
	if _, ok := findNode(buildAsOf(t, s, farFuture), s["LONER"]); ok {
		t.Error("a person with no evidence at all is never part of a world read")
	}
	if _, ok := findNode(buildAcme(t, s), s["LONER"]); !ok {
		t.Error("the current snapshot keeps him")
	}
	late := buildAsOf(t, s, time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC))
	if _, ok := findNode(late, s["BUYER"]); !ok {
		t.Error("the buyer exists once his first evidence is before T")
	}
}

// M3: a person's display name, title, merge and account are overwritten in place. A world read carries none
// of them, and a contact re-attributed to another account later still belongs to the account whose evidence
// names him.
func TestSnapshotAsOfTreatsNameAndAccountOfAPersonAsCurrentOnly(t *testing.T) {
	s := seedWorld(t)
	runSeed(t, s, []seedStep{
		{"", `UPDATE people SET account_id = $B, display_name = 'Renamed Later' WHERE id = $CHAMP`},
	})
	snap := buildAsOf(t, s, farFuture)
	champ, ok := findNode(snap, s["CHAMP"])
	if !ok {
		t.Fatal("the contact is evidenced in this account's claims and relationships")
	}
	if _, has := champ.Props["display_name"]; has {
		t.Error("display_name is overwritten in place: no value at a past time")
	}
	if champ.Props["account_id"] != s["A"] {
		t.Errorf("account_id = %v, want the account of the evidence %s, not the re-attribution", champ.Props["account_id"], s["A"])
	}
	if len(edgeByType(snap, RelChampionFor)) == 0 {
		t.Error("the champion edge of the evidenced contact must survive the re-attribution")
	}
	if _, ok := findNode(buildAcme(t, s), s["CHAMP"]); ok {
		t.Error("the current snapshot follows the current account")
	}
}

// The deal's buying-group roles come from the opportunity state version of that time, not the current one.
func TestSnapshotAsOfReadsRolesFromTheOpportunityStateOfThatTime(t *testing.T) {
	s := seedWorld(t)
	runSeed(t, s, []seedStep{{"LEGALX", `INSERT INTO people (kind, display_name, account_id) VALUES ('contact','Lena Legal',$A) RETURNING id`}})
	member := func(status string, at string) string {
		return `{"account_id":"` + s["A"] + `","opportunity_id":"` + s["OPP"] + `","version":1,"is_open":true,
 "buying_group":[{"person_id":"` + s["LEGALX"] + `","display_name":"Lena","roles":["technical_evaluator"],"status":"` + status + `",
   "evidence_refs":[{"activity_id":"` + s["ACT_CALL"] + `","occurred_at":"` + at + `"}]}]}`
	}
	v1, v2 := member("active", "2026-09-02T10:00:00Z"), member("departed", "2026-09-02T10:00:00Z")
	runSeed(t, s, []seedStep{
		{"", `INSERT INTO claims (account_id, opportunity_id, subject_person_id, field_path, value, standing, confidence, source_activity_id, evidence_quote, occurred_at, extractor)
 VALUES ($A,$OPP,$LEGALX,'buying_group.member','{"role":"technical_evaluator"}','first_party_ai',0.75,$ACT_CALL,'Lena evaluates','2026-09-02T10:00:00Z','llm:test@v1')`},
		{"", `INSERT INTO opportunity_state_history (opportunity_id, account_id, version, as_of, state) VALUES ($OPP,$A,1,'2026-09-02T10:00:00Z','` + v1 + `')`},
		{"", `INSERT INTO opportunity_state_history (opportunity_id, account_id, version, as_of, state) VALUES ($OPP,$A,2,'2026-09-05T10:00:00Z','` + v2 + `')`},
		{"", `INSERT INTO opportunity_state (opportunity_id, account_id, version, as_of, is_open, state) VALUES ($OPP,$A,2,'2026-09-05T10:00:00Z',true,'` + v2 + `')`},
	})
	status := func(at time.Time) string {
		edges := edgeByType(buildAsOf(t, s, at), RelTechnicalEvaluator)
		if len(edges) == 0 {
			return "absent"
		}
		return edges[0].Props["status"].(string)
	}
	for _, tc := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"at the first version's own time: strict, no state yet", callAt, "absent"},
		{"between the versions: the first, member active", time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), "open"},
		{"after the second: member departed, role closed", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), "closed"},
	} {
		if got := status(tc.at); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	if current := edgeByType(buildAcme(t, s), RelTechnicalEvaluator); len(current) != 1 || current[0].Props["status"] != "closed" {
		t.Errorf("the current snapshot reads the current state: %+v", current)
	}
}

// The in-memory sections honour the visibility rule like the Cypher ones: hidden activities are dropped and
// whatever cites one is withheld.
func TestWorldViewHonoursHiddenActivities(t *testing.T) {
	s := seedWorld(t)
	r := NewReader(nil, pg.DB, nil)
	view, err := r.NeighborhoodAsOf(context.Background(), s["A"], Params{SectionLimit: MaxSectionLimit, IncludeClosed: true},
		map[string]bool{s["ACT_HID"]: true}, farFuture)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Withheld {
		t.Error("the view must say something was withheld")
	}
	for _, n := range view.Nodes {
		if n.ID == s["ACT_HID"] {
			t.Error("a hidden activity must not be a node")
		}
		if n.ID == s["C_HID"] && n.Withheld != WithheldVisibility {
			t.Errorf("a claim citing a hidden activity must be a stub: %+v", n)
		}
	}
	for _, e := range view.Edges {
		for _, ref := range e.EvidenceRefs {
			if ref.ActivityID == s["ACT_HID"] {
				t.Errorf("edge %s cites a hidden activity", e.ID)
			}
		}
	}
}

func TestWorldViewSectionLimitTruncates(t *testing.T) {
	s := seedWorld(t)
	view, err := NewReader(nil, pg.DB, nil).NeighborhoodAsOf(context.Background(), s["A"], Params{SectionLimit: 1, IncludeClosed: true}, nil, farFuture)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Truncated || len(view.Sections[SectionClaims]) != 1 {
		t.Fatalf("truncated=%v claims=%v", view.Truncated, view.Sections[SectionClaims])
	}
}

func TestCurrentReadWithoutNeo4jIsUnavailableNotAPanic(t *testing.T) {
	s := seedWorld(t)
	if _, err := NewReader(nil, pg.DB, nil).Neighborhood(context.Background(), s["A"], Params{}, nil); err != ErrGraphUnavailable {
		t.Fatalf("err = %v, want ErrGraphUnavailable", err)
	}
}
