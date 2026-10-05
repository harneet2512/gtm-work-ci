package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// drainState recomputes every account with pending activity (a clock a day ahead makes every
// debounced job due) and returns the sum of the account state versions and the newest state date.
func drainState(t *testing.T, ctx context.Context, db *sql.DB) (versions int, newest time.Time) {
	t.Helper()
	svc, err := coalesce.New(db, coalesce.Options{Clock: clock.NewFixed(time.Now().Add(24 * time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}
	var max sql.NullTime
	if err := db.QueryRow(`SELECT coalesce(sum(version), 0), max(as_of) FROM account_state`).Scan(&versions, &max); err != nil {
		t.Fatal(err)
	}
	return versions, max.Time
}

func ingestFile(t *testing.T, path string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run([]string{"ingest", path}, &out); err != nil {
		t.Fatalf("ingest %s: %v\n%s", path, err, out.String())
	}
	return out.String()
}

// TestReplayRoundTripsThroughIngestAgainstAnUntilStore: an --until store, then the replay of the
// current deals fed through `ghostctl ingest`: the events are accepted, account state advances, and
// running the same replay again delivers every event a second time without creating an activity.
// A replay from a later point than the store was loaded to is refused, not silently gapped.
func TestReplayRoundTripsThroughIngestAgainstAnUntilStore(t *testing.T) {
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
	if err := run([]string{"crmarena-split", "--cutoff", "2023-11-01", sample, "split.json"}, &out); err != nil {
		t.Fatal(err)
	}

	// An earlier --until leaves a gap: the replay refuses to start after it.
	if err := run([]string{"import-crmarena", "--until", "2023-10-01T00:00:00Z", sample}, &out); err != nil {
		t.Fatal(err)
	}
	// Events of another connector, dated before the replay, must not mask the gap.
	if _, err := env.DB.Exec(`INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, connector, occurred_at, payload)
 SELECT 'crm', 'legacy:' || g, 'created', md5('a' || g) || md5('b' || g), 'legacy-fixture', timestamptz '2023-01-01' + g * interval '1 minute', '{}'::jsonb
 FROM generate_series(1, 1000) g`); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"replay-crmarena", "--split", "split.json", "--out", "gap.json", sample}, &out)
	if err == nil || !strings.Contains(err.Error(), "earlier --until") || !strings.Contains(err.Error(), "skip") {
		t.Fatalf("a replay after an earlier --until must be refused with the gap named: %v", err)
	}
	if err := run([]string{"replay-crmarena", "--skip-store-check", "--split", "split.json", "--out", "forced.json", sample}, &out); err != nil {
		t.Fatalf("--skip-store-check must bypass the refusal: %v", err)
	}

	// The matching cutoff fills the gap (idempotent import) and the replay is accepted.
	if err := run([]string{"import-crmarena", "--until", sampleCutoff, sample}, &out); err != nil {
		t.Fatal(err)
	}
	vBefore, _ := drainState(t, ctx, env.DB)
	var activitiesBefore int
	if err := env.DB.QueryRow(`SELECT count(*) FROM activities`).Scan(&activitiesBefore); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"replay-crmarena", "--split", "split.json", "--out", "replay.json", sample}, &out); err != nil {
		t.Fatalf("replay with a matching store: %v", err)
	}
	raw := mustRead(t, "replay.json")
	var events []normalize.SourceEvent
	if err := json.Unmarshal(raw, &events); err != nil || len(events) == 0 {
		t.Fatalf("replay file: %d events, %v", len(events), err)
	}

	first := ingestFile(t, "replay.json")
	if want := " new, 0 duplicate"; !strings.Contains(first, want) || strings.Contains(first, ": 0 new") {
		t.Fatalf("first replay delivery: %s", first)
	}
	vAfter, newest := drainState(t, ctx, env.DB)
	if vAfter <= vBefore {
		t.Errorf("account state did not advance: versions %d -> %d", vBefore, vAfter)
	}
	cut, _ := time.Parse(time.RFC3339, sampleCutoff)
	if newest.Before(cut) {
		t.Errorf("newest state is as of %s, still before the cutoff", newest)
	}
	var activitiesMid int
	if err := env.DB.QueryRow(`SELECT count(*) FROM activities`).Scan(&activitiesMid); err != nil {
		t.Fatal(err)
	}
	if activitiesMid <= activitiesBefore {
		t.Errorf("replay created no activity: %d -> %d", activitiesBefore, activitiesMid)
	}

	second := ingestFile(t, "replay.json")
	if !strings.Contains(second, ": 0 new,") {
		t.Errorf("re-running the replay must add nothing: %s", second)
	}
	var activitiesAfter, twice, others int
	if err := env.DB.QueryRow(`SELECT count(*) FROM activities`).Scan(&activitiesAfter); err != nil {
		t.Fatal(err)
	}
	if activitiesAfter != activitiesMid {
		t.Errorf("re-running the replay created activities: %d -> %d", activitiesMid, activitiesAfter)
	}
	if err := env.DB.QueryRow(`SELECT count(*) FROM source_events WHERE occurred_at >= $1 AND delivery_count = 2`, cut).Scan(&twice); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.QueryRow(`SELECT count(*) FROM source_events WHERE occurred_at >= $1 AND delivery_count <> 2`, cut).Scan(&others); err != nil {
		t.Fatal(err)
	}
	if twice != len(events) || others != 0 {
		t.Errorf("delivery_count = 2 on %d events (replay has %d), %d with another count", twice, len(events), others)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
