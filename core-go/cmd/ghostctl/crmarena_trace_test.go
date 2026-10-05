package main

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
)

// Ids of the sample (fixtures/crmarena_sample), read off the raw records.
const (
	traceEmailID    = "02sWt000001zh67IAA" // from a customer contact to the rep Akira Suzuki
	traceEmailDeal  = "006Wt000007B62xIAC"
	traceEmailAcct  = "001Wt00000PHVkAIAX"
	traceEmailFrom  = "003Wt00000JqrTXIAZ" // Contact kavita.reddy@securelinktech.com
	traceEmailToRep = "akira.suzuki@vendor.example"
	traceTaskID     = "00TWt000002yksxMAA" // owned by User 005Wt000003NEGjIAO (Chen Lixin)
	traceTaskDeal   = "006Wt000007B6cQIAS"
	traceTaskAcct   = "001Wt00000PGdBuIAL"
	traceTaskRep    = "chen.lixin@vendor.example"
)

func scanString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatalf("%s %v: %v", query, args, err)
	}
	return s
}

func mappedEntity(t *testing.T, db *sql.DB, kind, key string) string {
	t.Helper()
	return scanString(t, db, `SELECT entity_id::text FROM entity_source_mappings
 WHERE entity_type = $1 AND source_system = 'crm' AND source_key = $2 AND valid_to IS NULL`, kind, key)
}

func employeeID(t *testing.T, db *sql.DB, email string) string {
	t.Helper()
	return scanString(t, db, `SELECT id::text FROM people WHERE kind = 'employee' AND primary_email = $1`, email)
}

// traceActivity returns the activity of one raw record, checking it resolved to the given account and deal.
func traceActivity(t *testing.T, db *sql.DB, system, objectID, acct, deal string) string {
	t.Helper()
	var id, gotAcct, gotDeal sql.NullString
	err := db.QueryRow(`SELECT id::text, account_id::text, opportunity_id::text FROM activities
 WHERE source_system = $1 AND source_object_id = $2`, system, objectID).Scan(&id, &gotAcct, &gotDeal)
	if err != nil {
		t.Fatalf("activity %s/%s: %v", system, objectID, err)
	}
	if want := mappedEntity(t, db, "account", "account:"+acct); gotAcct.String != want {
		t.Errorf("%s resolved to account %s, want %s (%s)", objectID, gotAcct.String, want, acct)
	}
	if want := mappedEntity(t, db, "opportunity", "opp:"+deal); gotDeal.String != want {
		t.Errorf("%s resolved to opportunity %s, want %s (%s)", objectID, gotDeal.String, want, deal)
	}
	return id.String
}

func hasParticipant(t *testing.T, db *sql.DB, activity, person, role string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT count(*) FROM activity_participants WHERE activity_id = $1::uuid AND person_id = $2::uuid AND role = $3`,
		activity, person, role).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// assertRawRecordsTraceThroughIngest is the HAR-129 gate on the sample: one raw EmailMessage and one
// raw Task end at the exact account, person and opportunity their Salesforce ids name.
func assertRawRecordsTraceThroughIngest(t *testing.T, db *sql.DB) {
	t.Helper()
	email := traceActivity(t, db, "email", traceEmailID, traceEmailAcct, traceEmailDeal)
	if !hasParticipant(t, db, email, mappedEntity(t, db, "person", "contact:"+traceEmailFrom), "from") {
		t.Errorf("email %s: sender is not contact %s", traceEmailID, traceEmailFrom)
	}
	if !hasParticipant(t, db, email, employeeID(t, db, traceEmailToRep), "to") {
		t.Errorf("email %s: recipient is not the employee %s", traceEmailID, traceEmailToRep)
	}
	task := traceActivity(t, db, "crm", "task:"+traceTaskID, traceTaskAcct, traceTaskDeal)
	if !hasParticipant(t, db, task, employeeID(t, db, traceTaskRep), "actor") {
		t.Errorf("task %s: actor is not the employee %s", traceTaskID, traceTaskRep)
	}
}

// assertLegacyWorldGuard: a database holding a fixture-world account refuses the import unless
// --allow-mixed is given.
func assertLegacyWorldGuard(t *testing.T, db *sql.DB, sample string) {
	t.Helper()
	var account string
	if err := db.QueryRow(`INSERT INTO accounts (name) VALUES ('Acme (legacy fixture world)') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method)
 VALUES ('account', $1::uuid, 'crm', 'account:AC-4', 1, 'seed')`, account); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run([]string{"import-crmarena", "--full-timeline", sample}, &out)
	if err == nil || !strings.Contains(err.Error(), "--allow-mixed") {
		t.Fatalf("import into a database with legacy accounts: %v", err)
	}
	if strings.Contains(out.String(), "seeded") {
		t.Errorf("the guard fired after seeding: %s", out.String())
	}
	out.Reset()
	if err := run([]string{"import-crmarena", "--allow-mixed", "--full-timeline", sample}, &out); err != nil {
		t.Fatalf("--allow-mixed refused: %v", err)
	}
	if !strings.Contains(out.String(), "0 new") {
		t.Errorf("mixed re-import: %s", out.String())
	}
}
