package claimstest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

func fixtures(t *testing.T) string {
	t.Helper()
	dir, err := FindFixtures()
	if err != nil {
		t.Skipf("fixtures not present: %v", err)
	}
	return dir
}

func TestLoadGoldReadsTwelveCheckpointsInOrder(t *testing.T) {
	golds, err := LoadGold(fixtures(t))
	if err != nil || len(golds) != 12 {
		t.Fatalf("%d %v", len(golds), err)
	}
	if golds[0].Account != "acme" || golds[0].Checkpoint != 1 || golds[11].Account != "northstar" || golds[11].Checkpoint != 4 {
		t.Fatalf("order: first %s cp%d, last %s cp%d", golds[0].Account, golds[0].Checkpoint, golds[11].Account, golds[11].Checkpoint)
	}
	if _, err := LoadGold(t.TempDir()); err == nil {
		t.Fatal("an empty directory must be an error")
	}
	people := PeopleFrom(golds)
	if people["person:priya_shah"].Email != "priya.shah@acme.com" || people["person:dana_kim"].Email != "dana@ghostvendor.com" {
		t.Fatalf("people = %+v", people)
	}
}

func bodyOfFile(t *testing.T, fixtures, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtures, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	evs, err := ingest.DecodeEvents(raw)
	if err != nil || len(evs) != 1 {
		t.Fatalf("%s: %v", rel, err)
	}
	act, err := normalize.Normalize(evs[0])
	if err != nil {
		t.Fatal(err)
	}
	return act.BodyText()
}

func TestDerivedCandidatesQuoteTheirEvidenceVerbatim(t *testing.T) {
	fx := fixtures(t)
	golds, _ := LoadGold(fx)
	d, err := DeriveCandidates(golds, PeopleFrom(golds), nil)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for file, cands := range d.ByFile {
		body := bodyOfFile(t, fx, file)
		for _, c := range cands {
			total++
			if !strings.Contains(body, c.EvidenceQuote) {
				t.Errorf("%s: quote %q is not verbatim in the activity body", file, c.EvidenceQuote)
			}
		}
	}
	if total < 30 || len(d.Skipped) == 0 {
		t.Fatalf("candidates = %d, skipped = %d", total, len(d.Skipped))
	}
}

func TestDerivedCandidatesShapePersonDelegationAndConflictFacts(t *testing.T) {
	golds, _ := LoadGold(fixtures(t))
	d, _ := DeriveCandidates(golds, PeopleFrom(golds), nil)
	find := func(file string, f claims.FieldPath) claims.Candidate {
		for _, c := range d.ByFile[file] {
			if c.FieldPath == f {
				return c
			}
		}
		t.Fatalf("no %s candidate for %s", f, file)
		return claims.Candidate{}
	}
	champ := find("world/accounts/acme/events/012_call_scope_transcript.json", claims.FieldChampion)
	if champ.SubjectIdentity != "priya.shah@acme.com" || string(champ.Value) != `"priya.shah@acme.com"` {
		t.Fatalf("champion = %+v", champ)
	}
	del := find("world/accounts/northstar/events/029_email_elena_delegation.json", claims.FieldDelegation)
	var v map[string]string
	_ = json.Unmarshal(del.Value, &v)
	if v["from"] != "elena.vasquez@northstar.health" || v["to"] != "sam.okafor@northstar.health" || del.SubjectIdentity != v["from"] {
		t.Fatalf("delegation = %s", del.Value)
	}
	conf := find("world/accounts/beta/events/039_email_ravi_residency_request.json", claims.FieldNextMilestone)
	if conf.Confidence != 0.8 || !strings.Contains(conf.EvidenceQuote, "not ready to schedule a review session") {
		t.Fatalf("conflict candidate = %+v", conf)
	}
	eb := find("world/accounts/acme/events/014_crm_note_scoping.json", claims.FieldEconomicBuyer)
	if string(eb.Value) != `"unknown"` || eb.SubjectIdentity != "" {
		t.Fatalf("unknown economic buyer = %+v", eb)
	}
	role := find("live/acme_marco_soc2_email.json", claims.FieldStakeholderRole)
	if role.Role != "security" || role.SubjectIdentity != "marco.ruiz@acme.com" {
		t.Fatalf("role = %+v", role)
	}
	nm := find("live/acme_marco_soc2_email.json", claims.FieldNextMeeting)
	if string(nm.Value) != "null" {
		t.Fatalf("known-absent next_meeting = %s", nm.Value)
	}
}

func TestDeriveCandidatesRejectsBadFacts(t *testing.T) {
	q := "quote"
	mk := func(f CriticalFact) []Gold {
		return []Gold{{Account: "x", Checkpoint: 1, Expected: Expected{CriticalFacts: []CriticalFact{f}}}}
	}
	for name, f := range map[string]CriticalFact{
		"unknown field":        {Field: "nope", Value: json.RawMessage(`"x"`), EvidenceQuote: &q},
		"person without email": {Field: "champion", Value: json.RawMessage(`"person:ghost"`), EvidenceQuote: &q},
		"non-string champion":  {Field: "champion", Value: json.RawMessage(`4`), EvidenceQuote: &q},
		"role subject missing": {Field: "stakeholder_role", Subject: "person:ghost", Value: json.RawMessage(`"security"`), EvidenceQuote: &q},
		"bad delegation":       {Field: "delegation", Value: json.RawMessage(`"x"`), EvidenceQuote: &q},
		"delegation no email":  {Field: "delegation", Value: json.RawMessage(`{"from":"person:a","to":"person:b"}`), EvidenceQuote: &q},
	} {
		if _, err := DeriveCandidates(mk(f), People{}, nil); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	extra := map[string][]claims.Candidate{"f.json": {{FieldPath: claims.FieldHealth, Value: json.RawMessage(`"x"`), EvidenceQuote: "q", Confidence: 0.5}}}
	d, err := DeriveCandidates(nil, nil, extra)
	if err != nil || len(d.ByFile["f.json"]) != 1 {
		t.Fatalf("extras: %+v %v", d, err)
	}
}

func TestGoldExtractorAnswersPerActivityFileAndTheFakeCountsCalls(t *testing.T) {
	d := Derivation{ByFile: map[string][]claims.Candidate{"a.json": {{FieldPath: claims.FieldHealth, Value: json.RawMessage(`"x"`)}}}}
	ex := NewGoldExtractor(d, func(id string) string {
		if id == "act-a" {
			return "a.json"
		}
		return ""
	})
	resp, err := ex.Extract(context.Background(), claims.ExtractRequest{Activity: claims.ActivityInput{ID: "act-a"}, ExtractorVersion: "v"})
	if err != nil || len(resp.Claims) != 1 || resp.Model != "gold-fake" {
		t.Fatalf("%+v %v", resp, err)
	}
	if resp, _ := ex.Extract(context.Background(), claims.ExtractRequest{Activity: claims.ActivityInput{ID: "other"}}); len(resp.Claims) != 0 {
		t.Fatal("unknown activity must get no candidates")
	}
	if ex.CallsFor("act-a") != 1 || len(ex.Calls()) != 2 {
		t.Fatalf("calls = %d", len(ex.Calls()))
	}
}

func stateJSON(t *testing.T, mutate func(*reducer.AccountState)) []byte {
	t.Helper()
	st, _ := reducer.Reduce(reducer.Input{AccountID: "a", Version: 1, People: map[string]reducer.Person{}})
	if mutate != nil {
		mutate(&st)
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScoreStateKnowableMissesAndStatusNotes(t *testing.T) {
	q := "blocker quote"
	g := Gold{Account: "acme", Checkpoint: 1, AfterEventFile: "w/2", Expected: Expected{
		StateFields: map[string]json.RawMessage{
			"stage":        json.RawMessage(`"Discovery"`),
			"health":       json.RawMessage(`"on_track"`),
			"owner":        json.RawMessage(`"person:dana_kim"`),
			"blockers":     json.RawMessage(`[{"text":"Budget approval","status":"open"},{"text":"Hidden item","status":"open"}]`),
			"objections":   json.RawMessage(`"unknown"`),
			"next_meeting": json.RawMessage(`null`),
		},
		CriticalFacts: []CriticalFact{{Field: "blockers", Value: json.RawMessage(`"Budget approval"`), EvidenceEventFile: "w/1", EvidenceQuote: &q}},
	}}
	sc := Scorer{Gold: g, AccountGolds: []Gold{g}, Order: []string{"w/1", "w/2", "w/3"}, PersonID: map[string]string{"person:dana_kim": "uuid-dana"}}
	raw := stateJSON(t, func(st *reducer.AccountState) {
		st.Fields.Stage = reducer.Field{Value: "discovery", Known: true}
		st.Fields.Owner = reducer.Field{Value: "uuid-dana", Known: true}
		st.Fields.NextMeeting = reducer.Field{Value: nil, Known: true}
		st.Fields.Blockers = reducer.Field{Value: []reducer.Item{{Text: "budget approval", Status: "resolved"}}, Known: true}
	})
	facts, err := sc.ScoreState(raw)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Fact{}
	for _, f := range facts {
		by[f.Field] = f
	}
	if f := by["stage"]; !f.Correct || !f.Knowable || f.Reason != "" {
		t.Errorf("stage: %+v", f)
	}
	if f := by["owner"]; !f.Correct {
		t.Errorf("owner must compare through the person id map: %+v", f)
	}
	if f := by["next_meeting"]; !f.Correct || f.Want != "null" {
		t.Errorf("known-absent: %+v", f)
	}
	if f := by["health"]; f.Correct || f.Knowable || f.Reason == "" {
		t.Errorf("health has no critical fact and must be an unknowable miss: %+v", f)
	}
	if f := by["objections"]; !f.Correct || f.Knowable {
		t.Errorf("unknown list: %+v", f)
	}
	b := by["blockers"]
	if b.Correct || b.Knowable || !strings.Contains(b.Reason, "Hidden item") {
		t.Errorf("blockers lists an item no fact provides, so it is a non-knowable miss: %+v", b)
	}
	if c, n := Tally(facts, false); n != 6 || c != 4 {
		t.Errorf("tally = %d/%d", c, n)
	}
	if c, n := Tally(facts, true); n != 3 || c != 3 {
		t.Errorf("knowable tally = %d/%d", c, n)
	}
}

func TestScoreStateListStatusMismatchIsANoteNotAMiss(t *testing.T) {
	q := "x"
	g := Gold{Account: "a", Checkpoint: 1, AfterEventFile: "w/1", Expected: Expected{
		StateFields:   map[string]json.RawMessage{"current_commitments": json.RawMessage(`[{"text":"Send proposal","status":"fulfilled"}]`)},
		CriticalFacts: []CriticalFact{{Field: "commitment", Value: json.RawMessage(`"Send proposal"`), EvidenceEventFile: "w/1", EvidenceQuote: &q}},
	}}
	sc := Scorer{Gold: g, AccountGolds: []Gold{g}, Order: []string{"w/1"}}
	raw := stateJSON(t, func(st *reducer.AccountState) {
		st.Fields.CurrentCommitments = reducer.Field{Value: []reducer.Item{{Text: "Send proposal", Status: "open"}}, Known: true}
	})
	facts, _ := sc.ScoreState(raw)
	if len(facts) != 1 || !facts[0].Correct || !facts[0].Knowable || len(facts[0].Notes) != 1 {
		t.Fatalf("%+v", facts)
	}
}

func TestScoreBuyingGroup(t *testing.T) {
	g := Gold{Account: "a", Checkpoint: 1, Expected: Expected{
		BuyingGroup: []GoldMember{
			{PersonKey: "person:p", Roles: []string{"champion"}, Status: "active", Title: "Director"},
			{PersonKey: "person:q", Roles: []string{"user"}, Status: "new"},
			{PersonKey: "person:e", Roles: []string{"champion"}, Status: "active", DelegatedTo: "person:q"},
		},
		CoverageGaps: []string{"security"},
	}}
	sc := Scorer{Gold: g, PersonID: map[string]string{"person:p": "id-p", "person:q": "id-q", "person:e": "id-e"}}
	title := "Director"
	del := "id-q"
	raw := stateJSON(t, func(st *reducer.AccountState) {
		st.BuyingGroup = []reducer.Member{
			{PersonID: "id-p", Roles: []string{"champion"}, Status: "active", Title: &title},
			{PersonID: "id-e", Roles: []string{"champion"}, Status: "weakening", DelegatedToPerson: &del},
		}
		st.CoverageGaps = []string{"economic_buyer"}
	})
	facts, err := sc.ScoreBuyingGroup(raw)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Fact{}
	for _, f := range facts {
		by[f.Field] = f
	}
	for key, want := range map[string]bool{
		"buying_group:person:p:member": true, "buying_group:person:p:roles": true, "buying_group:person:p:title": true,
		"buying_group:person:q:member": false, "buying_group:person:e:status": false, "buying_group:person:e:delegated_to": true,
		"buying_group:*:coverage_gaps": false,
	} {
		f, ok := by[key]
		if !ok || f.Correct != want || (!want && f.Reason == "") {
			t.Errorf("%s: %+v", key, f)
		}
	}
	if _, ok := by["buying_group:person:q:roles"]; ok {
		t.Error("attributes of an absent member must not be scored separately")
	}
}

func TestIngestOrderPutsLiveFilesAfterWorldFilesKeepingTheirOrder(t *testing.T) {
	order := IngestOrder([]string{"w/2", "w/1"}, []string{"live/z", "live/a"})
	if strings.Join(order, ",") != "w/1,w/2,live/z,live/a" || Index(order, "live/a") != 3 || Index(order, "nope") != -1 {
		t.Fatalf("%v", order)
	}
}

func TestScoreStateRejectsGarbageState(t *testing.T) {
	if _, err := (Scorer{}).ScoreState([]byte(`{`)); err == nil {
		t.Fatal("garbage state accepted")
	}
	if _, err := (Scorer{}).ScoreBuyingGroup([]byte(`{`)); err == nil {
		t.Fatal("garbage state accepted")
	}
}

func TestScalarKnowableNeedsACriticalFactWithTheGoldValue(t *testing.T) {
	q := "quote"
	older := CriticalFact{Field: "decision_process", Value: json.RawMessage(`"Owen approved"`), EvidenceEventFile: "w/1", EvidenceQuote: &q}
	g := Gold{Account: "a", Checkpoint: 2, AfterEventFile: "w/2", Expected: Expected{
		StateFields:   map[string]json.RawMessage{"decision_process": json.RawMessage(`"Ravi must confirm"`)},
		CriticalFacts: []CriticalFact{older},
	}}
	sc := Scorer{Gold: g, AccountGolds: []Gold{g}, Order: []string{"w/1", "w/2"}}
	facts, err := sc.ScoreState(stateJSON(t, nil))
	if err != nil || len(facts) != 1 || facts[0].Knowable || facts[0].Correct || !strings.Contains(facts[0].Reason, "none carries the gold value") {
		t.Fatalf("an older, different value does not make the gold value knowable: %+v %v", facts, err)
	}
	g.Expected.CriticalFacts = append(g.Expected.CriticalFacts, CriticalFact{Field: "decision_process", Value: json.RawMessage(`"ravi MUST confirm"`), EvidenceEventFile: "w/2", EvidenceQuote: &q})
	sc.AccountGolds = []Gold{g}
	sc.Gold = g
	if facts, _ := sc.ScoreState(stateJSON(t, nil)); !facts[0].Knowable {
		t.Fatalf("the gold value is provided at this checkpoint: %+v", facts[0])
	}
}

func TestDeclaredLiveFilesAreReadFromTheGoldDocument(t *testing.T) {
	for name, tc := range map[string]struct {
		doc  string
		want string
	}{
		"top level":    {`{"live_files":["live/a.json","live/b.json"],"expected":{}}`, "live/a.json,live/b.json"},
		"under expect": {`{"expected":{"includes_live_files":["live/x.json"]}}`, "live/x.json"},
		"alt name":     {`{"live_event_files":["live/y.json"]}`, "live/y.json"},
		"none":         {`{"expected":{}}`, ""},
		"garbage":      {`[`, ""},
	} {
		if got := strings.Join(declaredLiveFiles([]byte(tc.doc)), ","); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestLiveOrderFollowsTheGoldDeclarationExactly(t *testing.T) {
	byTime := []string{"live/a_mnda.json", "live/a_marco.json", "live/a_extra.json"}
	cp3 := Gold{Account: "a", Checkpoint: 3}
	cp4 := Gold{Account: "a", Checkpoint: 4, LiveFiles: []string{"live/a_mnda.json", "live/a_marco.json"}}
	got, err := LiveOrder([]Gold{cp3, cp4}, byTime)
	if err != nil || strings.Join(got, ",") != "live/a_mnda.json,live/a_marco.json,live/a_extra.json" {
		t.Fatalf("declared order, then undeclared by time: %v %v", got, err)
	}
	// Declared order beats time order.
	swapped := Gold{Account: "a", Checkpoint: 4, LiveFiles: []string{"live/a_marco.json", "live/a_mnda.json"}}
	if got, _ := LiveOrder([]Gold{swapped}, byTime); strings.Join(got[:2], ",") != "live/a_marco.json,live/a_mnda.json" {
		t.Fatalf("the gold's list is the order: %v", got)
	}
	// No declaration: time order.
	if got, err := LiveOrder([]Gold{cp3}, byTime); err != nil || strings.Join(got, ",") != strings.Join(byTime, ",") {
		t.Fatalf("%v %v", got, err)
	}
	prefix := Gold{Account: "a", Checkpoint: 3, LiveFiles: []string{"live/a_mnda.json"}}
	if _, err := LiveOrder([]Gold{prefix, cp4}, byTime); err != nil {
		t.Fatalf("a prefix is consistent: %v", err)
	}
	clash := Gold{Account: "a", Checkpoint: 3, LiveFiles: []string{"live/a_marco.json"}}
	if _, err := LiveOrder([]Gold{clash, cp4}, byTime); err == nil {
		t.Fatal("conflicting declarations accepted")
	}
	ghost := Gold{Account: "a", Checkpoint: 4, LiveFiles: []string{"live/nope.json"}}
	if _, err := LiveOrder([]Gold{ghost}, byTime); err == nil {
		t.Fatal("a declared file that does not exist was accepted")
	}
}
