package main

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
)

// laterRecords lists, from the full timeline of the sample, the contacts and the deal-less records
// (orders, cases, chats) first seen at or after the cutoff: what --until must keep out of the store.
func laterRecords(t *testing.T, sample string, cut time.Time) (contacts, accountLevel []crmarena.Event) {
	t.Helper()
	snap, err := crmarena.Load(sample)
	if err != nil {
		t.Fatal(err)
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Events {
		if e.OccurredAt().Before(cut) {
			continue
		}
		switch {
		case strings.HasPrefix(e.Source.SourceObjectID, "contact:"):
			contacts = append(contacts, e)
		case e.DealID == "" && (strings.HasPrefix(e.Source.SourceObjectID, "order:") || e.Source.SourceSystem == "support"):
			accountLevel = append(accountLevel, e)
		}
	}
	return contacts, accountLevel
}

// assertNoFutureContactsOrAccountRecords: a contact first seen at or after the cutoff, and every
// order, case and chat dated then, are absent from the store (people, mappings and source events).
func assertNoFutureContactsOrAccountRecords(t *testing.T, db *sql.DB, sample string, cut time.Time) {
	t.Helper()
	contacts, accountLevel := laterRecords(t, sample, cut)
	if len(contacts) == 0 {
		t.Fatal("the sample must hold a contact first seen after the cutoff, or this proves nothing")
	}
	if len(accountLevel) == 0 { // the sample has no order, case or chat after the cutoff; asof_test covers them on a built snapshot
		t.Log("no deal-less record after the cutoff in the sample")
	}
	for _, e := range contacts {
		key := e.Source.SourceObjectID
		if n := countRows(t, db, `SELECT count(*) FROM entity_source_mappings WHERE entity_type = 'person' AND source_key = $1`, key); n != 0 {
			t.Errorf("%s first appears %s, after the cutoff, but is mapped to a person in the store", key, e.OccurredAt().Format(time.RFC3339))
		}
		if n := countRows(t, db, `SELECT count(*) FROM people WHERE primary_email = lower($1)`, contactEmail(t, e)); n != 0 {
			t.Errorf("%s (%s) is in people although first seen after the cutoff", key, contactEmail(t, e))
		}
	}
	for _, e := range accountLevel {
		s := e.Source
		if n := countRows(t, db, `SELECT count(*) FROM source_events WHERE source_system = $1 AND source_object_id = $2 AND source_event_key = $3`,
			s.SourceSystem, s.SourceObjectID, s.SourceEventKey); n != 0 {
			t.Errorf("deal-less record %s/%s/%s dated after the cutoff is in the store", s.SourceSystem, s.SourceObjectID, s.SourceEventKey)
		}
	}
}

func contactEmail(t *testing.T, e crmarena.Event) string {
	t.Helper()
	var p struct {
		Fields map[string]struct {
			New string `json:"new"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(e.Source.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p.Fields["Email"].New
}

// assertNoStoredPayloadDateAtOrAfter scans every stored source payload for a date-valued string at or
// after the cutoff (free text is not parsed).
func assertNoStoredPayloadDateAtOrAfter(t *testing.T, db *sql.DB, cut time.Time) {
	t.Helper()
	rows, err := db.Query(`SELECT source_object_id, source_event_key, payload::text FROM source_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	scanned := 0
	for rows.Next() {
		var obj, key, payload string
		if err := rows.Scan(&obj, &key, &payload); err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal([]byte(payload), &doc); err != nil {
			t.Fatal(err)
		}
		scanned++
		walkStrings(doc, "", func(path, s string) {
			for _, layout := range []string{"2006-01-02", time.RFC3339Nano, "2006-01-02T15:04:05.000-0700"} {
				if at, err := time.Parse(layout, s); err == nil && !at.Before(cut) {
					t.Errorf("stored payload %s/%s carries %s = %s, at or after the cutoff", obj, key, path, s)
				}
			}
		})
	}
	if err := rows.Err(); err != nil || scanned == 0 {
		t.Fatalf("scanned %d payloads: %v", scanned, err)
	}
}

func walkStrings(v any, path string, visit func(path, s string)) {
	switch x := v.(type) {
	case string:
		visit(path, x)
	case map[string]any:
		for k, child := range x {
			walkStrings(child, path+"."+k, visit)
		}
	case []any:
		for _, child := range x {
			walkStrings(child, path+"[]", visit)
		}
	}
}
