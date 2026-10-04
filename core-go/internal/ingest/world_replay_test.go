package ingest_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// loadFixtureEvents reads every SourceEvent in fixtures/world, then fixtures/live, each in
// lexical path order (the WP1 convention: file order is ingest order). Files that are not
// SourceEvents (org.json, gold checkpoints) are skipped. The directories are optional.
func loadFixtureEvents(t *testing.T) []ingest.NamedEvent {
	t.Helper()
	var events []ingest.NamedEvent
	for _, dir := range []string{worldDir, liveDir} {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		var paths []string
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".json") {
				paths = append(paths, path)
			}
			return err
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
		sort.Strings(paths)
		for _, p := range paths {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if !looksLikeSourceEvents(raw) {
				continue
			}
			evs, err := ingest.DecodeEvents(raw)
			if err != nil {
				t.Errorf("%s: %v", p, err)
				continue
			}
			for i, ev := range evs {
				events = append(events, ingest.NamedEvent{File: p, Index: i, Event: ev})
			}
		}
	}
	return events
}

func requireFixtures(t *testing.T) []ingest.NamedEvent {
	t.Helper()
	events := loadFixtureEvents(t)
	if len(events) == 0 {
		if os.Getenv("CI") == "true" {
			t.Fatalf("no SourceEvent JSON under %s or %s: the fixtures must exist in CI", worldDir, liveDir)
		}
		t.Skipf("no SourceEvent JSON under %s or %s (WP1 fixtures not present)", worldDir, liveDir)
	}
	return events
}

// Every fixture event must satisfy the normalizer (the contract between WP1 and WP2).
func TestEveryFixtureEventNormalizes(t *testing.T) {
	events := requireFixtures(t)
	types := map[string]int{}
	for _, ne := range events {
		act, err := normalize.Normalize(ne.Event)
		if err != nil {
			t.Errorf("%s#%d (%s/%s/%s): %v", ne.File, ne.Index, ne.Event.SourceSystem, ne.Event.SourceObjectID, ne.Event.SourceEventKey, err)
			continue
		}
		types[act.Type()]++
	}
	t.Logf("%d fixture events normalize; activity types: %v", len(events), types)
}

// With an empty entity graph (nothing for the resolver to match) every activity must be parked
// as unresolved, and replay must still be idempotent.
func TestReplayingFixturesWithoutAnyEntitiesParksEverythingAsUnresolved(t *testing.T) {
	events := requireFixtures(t)
	resetDB(t)
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})

	counts := replayTwice(t, svc, events, nil)

	// Domain hints cannot match any account; the only way to resolve is an existing mapping.
	if counts["recompute_jobs"] != 0 || counts["unresolved_activities"] != counts["activities"] {
		t.Fatalf("expected everything unresolved and no outbox rows, got %v", counts)
	}
}

// Acceptance (HAR-100): replaying all fixtures twice gives identical row counts.
func TestReplayingWorldAndLiveFixturesTwiceGivesIdenticalRowCounts(t *testing.T) {
	events := requireFixtures(t)
	resetDB(t)
	seedWorldOrg(t)
	svc := newService(t, clock.NewFixed(t0), ingest.Options{Extension: graph.NewExtension()})

	counts := replayTwice(t, svc, events, func() {
		// After the first pass only: the intentionally duplicated Acme transcript (012 and
		// 013_dup carry the same identity triple) is ONE activity delivered twice.
		const dup = `source_system = 'call' AND source_object_id = 'call-acme-0825' AND source_event_key = 'transcript_ready'`
		if got := queryString(t, `SELECT count(*)::text FROM source_events WHERE `+dup); got != "1" {
			t.Errorf("duplicate transcript produced %s source events, want 1", got)
		}
		if got := queryString(t, `SELECT delivery_count::text FROM source_events WHERE `+dup); got != "2" {
			t.Errorf("duplicate transcript delivery_count = %s, want 2", got)
		}
		if got := queryString(t, `SELECT count(*)::text FROM activities a JOIN source_events s ON s.id = a.source_event_id
			WHERE s.source_system = 'call' AND s.source_object_id = 'call-acme-0825' AND s.source_event_key = 'transcript_ready'`); got != "1" {
			t.Errorf("duplicate transcript produced %s activities, want 1", got)
		}
	})

	if counts["source_events"] != uniqueTriples(events) {
		t.Errorf("source_events = %d, want %d", counts["source_events"], uniqueTriples(events))
	}
	if counts["unresolved_activities"] != 0 {
		t.Errorf("%d fixture activities stayed unresolved although entity resolution (WP5) creates their accounts, people and mappings", counts["unresolved_activities"])
	}
	if counts["recompute_jobs"] < 3 {
		t.Errorf("recompute_jobs = %d, want a pending job for each of the 3 accounts", counts["recompute_jobs"])
	}
	t.Logf("replayed %d fixture deliveries twice (%d unique events): %v", len(events), uniqueTriples(events), counts)
	resolved := queryString(t, `SELECT count(*) FILTER (WHERE account_id IS NOT NULL)::text || '/' || count(*)::text FROM activities`)
	t.Logf("activities with an account: %s; with an opportunity: %s; participants linked to a person: %s", resolved,
		queryString(t, `SELECT count(*)::text FROM activities WHERE opportunity_id IS NOT NULL`),
		queryString(t, `SELECT count(*)::text FROM activity_participants WHERE person_id IS NOT NULL`))
}

// seedWorldOrg seeds the vendor's employees from fixtures/world/org.json, as `ghostctl seed-org` does.
func seedWorldOrg(t *testing.T) {
	t.Helper()
	org, err := graph.LoadCompany(filepath.Join(worldDir, "org.json"))
	if err != nil {
		t.Fatalf("load org: %v", err)
	}
	if _, err := graph.SeedCompany(context.Background(), env.DB, org, t0); err != nil {
		t.Fatalf("seed org: %v", err)
	}
}
