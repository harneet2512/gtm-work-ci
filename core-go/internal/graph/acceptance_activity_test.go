package graph_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

func TestWorldActivityEdgesMatchAnIndependentExpectation(t *testing.T) {
	ingestWorld(t)
	w := buildWorld(t)
	events := fixtureEvents(t)

	activityID := map[[3]string]string{}
	rows, err := env.DB.Query(`SELECT s.source_system, s.source_object_id, s.source_event_key, a.id::text
		FROM activities a JOIN source_events s ON s.id = a.source_event_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s, o, k, id string
		if err := rows.Scan(&s, &o, &k, &id); err != nil {
			t.Fatal(err)
		}
		activityID[[3]string{s, o, k}] = id
	}
	rows.Close()

	acts := make([]normalize.Activity, len(events))
	calendarOpp := map[string]string{} // calendar event id -> opportunity identity
	for i, ev := range events {
		a, err := normalize.Normalize(ev)
		if err != nil {
			t.Fatal(err)
		}
		acts[i] = a
		if ev.SourceSystem == "calendar" {
			for _, h := range a.OpportunityHints() {
				calendarOpp[a.SourceObjectID()] = "crm:" + h.Value
			}
		}
	}

	wantPart, wantAbout, wantInv := map[[2]string]bool{}, map[[2]string]bool{}, map[[2]string]bool{}
	scoredAbout := map[string]bool{}
	for i, ev := range events {
		a := acts[i]
		id := activityID[[3]string{ev.SourceSystem, ev.SourceObjectID, ev.SourceEventKey}]
		for _, p := range a.Participants() {
			identity := p.RawIdentity
			if strings.Contains(identity, "@") {
				identity = "email:" + identity
			}
			key, ok := w.g.keyOfID[identity]
			if !ok {
				continue
			}
			wantInv[[2]string{id, key}] = true // every participant, mentioned parties included
			switch p.Role {
			case normalize.RoleActor, normalize.RoleFrom, normalize.RoleTo, normalize.RoleCC, normalize.RoleAttendee, normalize.RoleOrganizer, normalize.RoleSpeaker:
				wantPart[[2]string{key, id}] = true
			}
		}
		// The activity's opportunity: its own reference, else (calls) its calendar event's.
		oppIdentity := ""
		for _, h := range a.OpportunityHints() {
			if h.Kind == normalize.HintCRM {
				oppIdentity = "crm:" + h.Value
			}
		}
		if oppIdentity == "" && ev.SourceSystem == "call" {
			var c struct {
				CalendarEventID *string `json:"calendar_event_id"`
			}
			if err := json.Unmarshal(ev.Payload, &c); err == nil && c.CalendarEventID != nil {
				oppIdentity = calendarOpp[*c.CalendarEventID]
			}
		}
		target := w.g.keyOfID[oppIdentity]
		if target == "" { // else the account named by a CRM id or a domain; slack/calendar hints are unscored
			for _, h := range a.AccountHints() {
				switch h.Kind {
				case normalize.HintCRM:
					target = w.g.keyOfID["crm:"+h.Value]
				case normalize.HintDomain:
					target = w.g.keyOfID["domain:"+h.Value]
				}
				if target != "" {
					break
				}
			}
		}
		if target == "" {
			continue
		}
		scoredAbout[id] = true
		wantAbout[[2]string{id, target}] = true
	}

	gotPart, gotAbout, gotInv := map[[2]string]bool{}, map[[2]string]bool{}, map[[2]string]bool{}
	er, err := env.DB.Query(`SELECT src_type, src_id::text, rel_type, dst_type, dst_id::text FROM relationships
		WHERE valid_to IS NULL AND rel_type IN ('participated_in', 'about', 'involves')`)
	if err != nil {
		t.Fatal(err)
	}
	defer er.Close()
	for er.Next() {
		var st, sid, rel, dt, did string
		if err := er.Scan(&st, &sid, &rel, &dt, &did); err != nil {
			t.Fatal(err)
		}
		switch rel {
		case "participated_in":
			gotPart[[2]string{w.keyOf(st, sid), did}] = true
		case "about":
			if scoredAbout[sid] {
				gotAbout[[2]string{sid, w.keyOf(dt, did)}] = true
			}
		case "involves":
			gotInv[[2]string{sid, w.keyOf(dt, did)}] = true
		}
	}

	for name, pair := range map[string][2]map[[2]string]bool{
		"participated_in": {wantPart, gotPart}, "about": {wantAbout, gotAbout}, "involves": {wantInv, gotInv},
	} {
		s := scoreSets(pair[0], pair[1])
		t.Logf("%s: expected %d, predicted %d, precision %.4f, recall %.4f (expectation derived from fixture participants and opportunity/account references)", name, s.gold, s.predicted, s.precision(), s.recall())
		if s.gold == 0 {
			t.Errorf("%s: empty expectation", name)
		}
		for k := range pair[0] {
			if !pair[1][k] {
				t.Logf("  %s missing %v", name, k)
			}
		}
		for k := range pair[1] {
			if !pair[0][k] {
				t.Logf("  %s unexpected %v", name, k)
			}
		}
		if s.precision() < minScore || s.recall() < minScore {
			t.Errorf("%s precision %.4f / recall %.4f below %.2f", name, s.precision(), s.recall(), minScore)
		}
	}
}
