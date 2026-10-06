package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// buyingGroupHas reads the account's current state and reports whether the person is a member.
func buyingGroupHas(t *testing.T, db *sql.DB, account, person string) (version int, has bool) {
	t.Helper()
	var raw []byte
	if err := db.QueryRow(`SELECT version, state FROM account_state WHERE account_id = $1::uuid`, account).Scan(&version, &raw); err != nil {
		t.Fatalf("account state of %s: %v", account, err)
	}
	var st reducer.AccountState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	for _, m := range st.BuyingGroup {
		if m.PersonID == person {
			return version, true
		}
	}
	return version, false
}

// TestDeliveredContactBecomesAPersonAndABuyingGroupChange: after an --until import, a stakeholder who first
// writes on a current deal is delivered by the replay; ingesting it creates the person, the account's
// buying group gains the member (a state diff naming buying_group), and the email that follows resolves
// to that person. State diffs and signal emission ("stakeholder entered") are not built yet (no code writes
// state_diffs or signals): HAR-106 should extend this test with the diff row and the signal once they exist;
// until then the person and the buying-group delta between state versions are the contract.
func TestDeliveredContactBecomesAPersonAndABuyingGroupChange(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	env, err := storetest.Start(ctx)
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	t.Setenv("DATABASE_URL", env.URL)
	sample := abs(t, crmarenaSample)
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	if err := run([]string{"crmarena-split", "--cutoff", "2023-11-01", sample, "split0.json"}, &out); err != nil {
		t.Fatal(err)
	}
	split, err := readSplit("split0.json")
	if err != nil {
		t.Fatal(err)
	}
	dir, id := copyWithLateContact(t, sample, split)
	if err := run([]string{"crmarena-split", "--cutoff", "2023-11-01", dir, "split.json"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"import-crmarena", "--until", sampleCutoff, dir}, &out); err != nil {
		t.Fatal(err)
	}
	drainState(t, ctx, env.DB)
	email := "lena@" + lateDomain(t, dir)
	if n := countRows(t, env.DB, `SELECT count(*) FROM people WHERE lower(primary_email) = $1`, email); n != 0 {
		t.Fatalf("the late stakeholder is already a person after the --until import")
	}

	if err := run([]string{"replay-crmarena", "--split", "split.json", "--out", "replay.json", dir}, &out); err != nil {
		t.Fatal(err)
	}
	ingestFile(t, "replay.json")
	drainState(t, ctx, env.DB)

	person := mappedEntity(t, env.DB, "person", "contact:"+id)
	if got := scanString(t, env.DB, `SELECT id::text FROM people WHERE lower(primary_email) = $1`, email); got != person {
		t.Fatalf("person for %s is %s, the contact mapping says %s", email, got, person)
	}
	// The email that names the contact resolves to the new person as its sender.
	activity := scanString(t, env.DB, `SELECT id::text FROM activities WHERE source_object_id = '02sLATE0000000001'`)
	if !hasParticipant(t, env.DB, activity, person, "from") {
		t.Errorf("the contact's first email does not resolve to person %s", person)
	}
	account := scanString(t, env.DB, `SELECT account_id::text FROM activities WHERE id = $1::uuid`, activity)
	version, has := buyingGroupHas(t, env.DB, account, person)
	if !has {
		t.Fatalf("the buying group of account %s (version %d) does not contain the delivered contact", account, version)
	}
	// The buying-group delta between the state before and after the replay: exactly the new person.
	if version < 2 {
		t.Fatalf("state is at version %d: the replay did not advance it", version)
	}
	before, after := historyMembers(t, env.DB, account, version-1), historyMembers(t, env.DB, account, version)
	var added []string
	for p := range after {
		if !before[p] {
			added = append(added, p)
		}
	}
	if len(added) != 1 || added[0] != person {
		t.Errorf("buying-group members added between versions %d and %d: %v, want only %s", version-1, version, added, person)
	}
	for p := range before {
		if !after[p] {
			t.Errorf("member %s was dropped by the replay", p)
		}
	}
}

// historyMembers lists the buying-group members of one state_history version.
func historyMembers(t *testing.T, db *sql.DB, account string, version int) map[string]bool {
	t.Helper()
	var raw []byte
	if err := db.QueryRow(`SELECT state FROM state_history WHERE account_id = $1::uuid AND version = $2`, account, version).Scan(&raw); err != nil {
		t.Fatalf("state version %d of %s: %v", version, account, err)
	}
	var st reducer.AccountState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, m := range st.BuyingGroup {
		out[m.PersonID] = true
	}
	return out
}

// lateDomain is the email domain of the contacts of the copy's first account with the late contact.
func lateDomain(t *testing.T, dir string) string {
	t.Helper()
	var contacts []map[string]any
	readJSON(t, dir+"/Contact.json", &contacts)
	for _, c := range contacts {
		if c["Id"] == "003LATE0000000001" {
			addr := c["Email"].(string)
			return addr[strings.Index(addr, "@")+1:]
		}
	}
	t.Fatal("late contact not found")
	return ""
}
