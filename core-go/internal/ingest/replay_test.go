package ingest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
)

const (
	testdataDir = "testdata"
	worldDir    = "../../../fixtures/world" // WP1; optional
	liveDir     = "../../../fixtures/live"  // WP1; optional
	schemasDir  = "../../../contracts/schemas"
)

// seedAcmeWorld seeds the identities the testdata fixtures refer to.
func seedAcmeWorld(t *testing.T) (acct string) {
	t.Helper()
	acct = seedAccount(t, "Acme", "acme.com")
	opp := seedOpportunity(t, acct, "EU expansion")
	marco := seedPerson(t, "Marco Ruiz", "marco.ruiz@acme.com")
	seedMapping(t, "account", acct, "crm", "account:AC-4")
	seedMapping(t, "account", acct, "slack", "#deal-acme")
	seedMapping(t, "opportunity", opp, "crm", "opp:AC-4-EXP")
	seedMapping(t, "person", marco, "email", "marco.ruiz@acme.com")
	seedMapping(t, "person", marco, "crm", "contact:817")
	return acct
}

// uniqueTriples counts distinct (source_system, source_object_id, source_event_key) triples:
// fixtures may contain deliberate duplicate deliveries.
func uniqueTriples(events []ingest.NamedEvent) int {
	seen := map[[3]string]struct{}{}
	for _, ne := range events {
		seen[[3]string{ne.Event.SourceSystem, ne.Event.SourceObjectID, ne.Event.SourceEventKey}] = struct{}{}
	}
	return len(seen)
}

// replayTwice ingests events, snapshots row counts, ingests them all again and asserts the
// second pass was entirely duplicate and left every row count identical. afterFirstPass, when
// non-nil, runs between the two passes. It returns the final row counts.
func replayTwice(t *testing.T, svc *ingest.Service, events []ingest.NamedEvent, afterFirstPass func()) map[string]int {
	t.Helper()
	ctx := context.Background()
	unique := uniqueTriples(events)

	first, err := ingest.IngestAll(ctx, svc, events)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if first.New != unique || first.New+first.Duplicate != len(events) {
		t.Fatalf("first pass summary %+v, want %d new out of %d deliveries", first, unique, len(events))
	}
	afterFirst := tableCounts(t)
	if afterFirstPass != nil {
		afterFirstPass()
	}

	second, err := ingest.IngestAll(ctx, svc, events)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.New != 0 || second.Duplicate != len(events) {
		t.Fatalf("second pass summary %+v, want %d duplicates", second, len(events))
	}
	afterSecond := tableCounts(t)
	if !reflect.DeepEqual(afterFirst, afterSecond) {
		t.Fatalf("row counts differ after replay: first %v, second %v", afterFirst, afterSecond)
	}
	if afterSecond["source_events"] != unique || afterSecond["activities"] != unique {
		t.Fatalf("want %d source_events and activities, got %v", unique, afterSecond)
	}
	if got := queryString(t, `SELECT sum(delivery_count)::text FROM source_events`); got != fmt.Sprint(2*len(events)) {
		t.Fatalf("sum(delivery_count) = %s, want %d (every delivery counted exactly once)", got, 2*len(events))
	}
	return afterSecond
}

func TestReplayingFixturesTwiceGivesIdenticalRowCounts(t *testing.T) {
	resetDB(t)
	seedAcmeWorld(t)
	events, err := ingest.LoadEvents(testdataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 15 {
		t.Fatalf("testdata too small: %d events", len(events))
	}
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})

	counts := replayTwice(t, svc, events, nil)

	if counts["source_events"] != len(events) || counts["activities"] != len(events) {
		t.Errorf("1:1 source_events/activities expected, got %v for %d events", counts, len(events))
	}
	if counts["unresolved_activities"] != 1 {
		t.Errorf("unresolved = %d, want 1 (the newco.io email)", counts["unresolved_activities"])
	}
	if counts["recompute_jobs"] != 1 {
		t.Errorf("recompute_jobs = %d, want 1 pending job for Acme", counts["recompute_jobs"])
	}
	// Everything but the newco email belongs to Acme and sits in the one pending job.
	if got := queryString(t, `SELECT cardinality(activity_ids)::text FROM recompute_jobs`); got != "16" {
		t.Errorf("job holds %s activities, want 16", got)
	}
	if got := queryString(t, `SELECT count(*)::text FROM activities WHERE opportunity_id IS NOT NULL`); got == "0" {
		t.Error("no activity resolved an opportunity")
	}
	if got := queryString(t, `SELECT count(*)::text FROM activity_participants WHERE person_id IS NOT NULL`); got == "0" {
		t.Error("no participant linked to a person")
	}
}

// looksLikeSourceEvents is true for a JSON object or array of objects that carry a
// source_system key.
func looksLikeSourceEvents(raw []byte) bool {
	var one map[string]json.RawMessage
	if json.Unmarshal(raw, &one) == nil {
		_, ok := one["source_system"]
		return ok
	}
	var many []map[string]json.RawMessage
	if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
		_, ok := many[0]["source_system"]
		return ok
	}
	return false
}

func TestLooksLikeSourceEvents(t *testing.T) {
	cases := map[string]bool{
		`{"source_system":"email"}`:   true,
		`[{"source_system":"email"}]`: true,
		`{"checkpoint":1}`:            false,
		`[{"x":1}]`:                   false,
		`[]`:                          false,
		`"s"`:                         false,
		`nope`:                        false,
	}
	for in, want := range cases {
		if got := looksLikeSourceEvents([]byte(in)); got != want {
			t.Errorf("looksLikeSourceEvents(%s) = %v, want %v", in, got, want)
		}
	}
}

// The hand-written testdata must itself conform to the frozen contracts, otherwise the replay
// test would prove nothing about real connector output.
func TestTestdataConformsToContractSchemas(t *testing.T) {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	files, err := filepath.Glob(filepath.Join(schemasDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no schemas in %s: %v", schemasDir, err)
	}
	for _, f := range files {
		doc := readSchemaJSON(t, f)
		if err := c.AddResource(doc.(map[string]any)["$id"].(string), doc); err != nil {
			t.Fatalf("add %s: %v", f, err)
		}
	}
	envelope, err := c.Compile("https://ghost.local/contracts/source_event.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := c.Compile("https://ghost.local/contracts/source_payloads.v1.json")
	if err != nil {
		t.Fatal(err)
	}

	paths, _ := filepath.Glob(filepath.Join(testdataDir, "*.json"))
	if len(paths) == 0 {
		t.Fatal("no testdata files")
	}
	for _, p := range paths {
		doc := readSchemaJSON(t, p)
		items, ok := doc.([]any)
		if !ok {
			items = []any{doc}
		}
		for i, item := range items {
			if err := envelope.Validate(item); err != nil {
				t.Errorf("%s#%d violates source_event.v1.json: %v", p, i, err)
				continue
			}
			if err := payloads.Validate(item.(map[string]any)["payload"]); err != nil {
				t.Errorf("%s#%d payload violates source_payloads.v1.json: %v", p, i, err)
			}
		}
	}
}

func readSchemaJSON(t *testing.T, path string) any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}
