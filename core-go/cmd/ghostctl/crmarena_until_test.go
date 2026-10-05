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
	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// sampleCutoff is the frozen split's cutoff (bench/data/deal_split.json), as the --until value HAR-129
// runs use.
const sampleCutoff = "2023-11-01T00:00:00Z"

func TestImportCRMArenaRequiresAnExplicitTimeline(t *testing.T) {
	sample := abs(t, crmarenaSample)
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "")
	var out bytes.Buffer
	err := run([]string{"import-crmarena", sample}, &out)
	if err == nil || !strings.Contains(err.Error(), "--until") || !strings.Contains(err.Error(), "--full-timeline") {
		t.Fatalf("an import without a timeline choice must name both flags: %v", err)
	}
	if err := run([]string{"import-crmarena", "--dry-run", sample}, &out); err == nil {
		t.Error("a dry run without a timeline choice accepted: the printed counts would differ from the real run")
	}
	err = run([]string{"import-crmarena", "--full-timeline", "--until", sampleCutoff, sample}, &out)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("both flags accepted: %v", err)
	}
	err = run([]string{"import-crmarena", "--until", "2023-11-01", sample}, &out)
	if err == nil || !strings.Contains(err.Error(), "RFC 3339") {
		t.Errorf("a date without a time zone accepted: %v", err)
	}
}

func TestImportDryRunUntilCutsTheTimeline(t *testing.T) {
	sample := abs(t, crmarenaSample)
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "")
	var out bytes.Buffer
	if err := run([]string{"import-crmarena", "--dry-run", "--until", sampleCutoff, sample}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "timeline cut at "+sampleCutoff) || strings.Contains(out.String(), "built 336 events") {
		t.Fatalf("dry run output: %s", out.String())
	}
}

// replayed runs replay-crmarena and decodes its JSON array.
func replayed(t *testing.T, args ...string) []normalize.SourceEvent {
	t.Helper()
	var out bytes.Buffer
	if err := run(append([]string{"replay-crmarena"}, args...), &out); err != nil {
		t.Fatalf("replay-crmarena %v: %v", args, err)
	}
	var got []normalize.SourceEvent
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("replay output is not a SourceEvent array: %v", err)
	}
	return got
}

func TestReplayCRMArenaFeedsTheRemainingEventsInOrder(t *testing.T) {
	sample := abs(t, crmarenaSample)
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "")
	var out bytes.Buffer
	if err := run([]string{"crmarena-split", "--cutoff", "2023-11-01", sample, "split.json"}, &out); err != nil {
		t.Fatal(err)
	}
	cut, _ := time.Parse(time.RFC3339, sampleCutoff)
	all := replayed(t, "--from", sampleCutoff, sample)
	if len(all) == 0 {
		t.Fatal("nothing remains after the cutoff")
	}
	for i, e := range all {
		if e.OccurredAt.Before(cut) || (i > 0 && e.OccurredAt.Before(*all[i-1].OccurredAt)) {
			t.Fatalf("event %d (%s) at %s is before the cutoff or out of order", i, e.SourceObjectID, e.OccurredAt)
		}
	}
	mine := replayed(t, "--split", "split.json", sample) // --from defaults to the split's cutoff
	if len(mine) == 0 || len(mine) > len(all) {
		t.Fatalf("current deals' remaining events: %d of %d", len(mine), len(all))
	}
	snap, _ := crmarena.Load(sample)
	res, _ := crmarena.Build(snap)
	if before, _, _ := crmarena.AsOf(res.Events, cut); len(before)+len(all) != len(res.Events) {
		t.Errorf("import --until (%d) and replay --from (%d) do not partition the %d events", len(before), len(all), len(res.Events))
	}
	if err := run([]string{"replay-crmarena", "--split", "split.json", "--deal", "no-such-deal", sample}, &out); err == nil {
		t.Error("a deal that is not current accepted")
	}
	if err := run([]string{"replay-crmarena", "--out", "r.json", "--split", "split.json", sample}, &out); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile("r.json"); err != nil || !strings.HasPrefix(string(raw), "[") {
		t.Errorf("--out file: %v", err)
	}
}

// currentDealEvents lists the events that must not be in the store after --until: every event of a
// current deal dated at or after the cutoff, which includes its final stage and its quotes' status.
func currentDealEvents(t *testing.T, sample string, cut time.Time) (current map[string]bool, withheld []crmarena.Event, snapshotOnly int) {
	t.Helper()
	snap, err := crmarena.Load(sample)
	if err != nil {
		t.Fatal(err)
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		t.Fatal(err)
	}
	windows, _ := crmarena.Windows(snap)
	split, err := crmarena.NewSplit(windows, cut.Format("2006-01-02"), 0)
	if err != nil {
		t.Fatal(err)
	}
	current = split.CurrentSet()
	for _, e := range res.Events {
		if current[e.DealID] && !e.OccurredAt().Before(cut) {
			withheld = append(withheld, e)
			if k := e.Source.SourceEventKey; strings.HasPrefix(k, "field:StageName:") || strings.HasPrefix(k, "field:Status:") {
				snapshotOnly++
			}
		}
	}
	return current, withheld, snapshotOnly
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// TestImportUntilKeepsTheOutcomeOutOfTheStore is the HAR-129 gate on the sample: after
// import-crmarena --until <cutoff>, no current deal's final stage, quote status or post-cutoff event
// is in the store, and the AccountState of each current deal's account is built from pre-cutoff facts.
func TestImportUntilKeepsTheOutcomeOutOfTheStore(t *testing.T) {
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
	cut, _ := time.Parse(time.RFC3339, sampleCutoff)
	current, withheld, snapshotOnly := currentDealEvents(t, sample, cut)
	if len(current) == 0 || len(withheld) == 0 || snapshotOnly == 0 {
		t.Fatalf("the sample must have current deals with post-cutoff events and snapshot-only values (%d, %d, %d), or this test proves nothing",
			len(current), len(withheld), snapshotOnly)
	}

	var out bytes.Buffer
	if err := run([]string{"import-crmarena", "--until", sampleCutoff, sample}, &out); err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	for _, table := range []string{"source_events", "activities"} {
		if n := countRows(t, env.DB, `SELECT count(*) FROM `+table+` WHERE occurred_at >= $1`, cut); n != 0 {
			t.Errorf("%d %s dated at or after the cutoff were ingested", n, table)
		}
	}
	for _, e := range withheld {
		s := e.Source
		if n := countRows(t, env.DB, `SELECT count(*) FROM source_events WHERE source_system = $1 AND source_object_id = $2 AND source_event_key = $3`,
			s.SourceSystem, s.SourceObjectID, s.SourceEventKey); n != 0 {
			t.Errorf("withheld event %s/%s/%s of deal %s is in the store", s.SourceSystem, s.SourceObjectID, s.SourceEventKey, e.DealID)
		}
	}
	for deal := range current {
		if n := countRows(t, env.DB, `SELECT count(*) FROM source_events WHERE source_object_id = $1 AND source_event_key LIKE 'field:StageName:%'`, "opp:"+deal); n != 0 {
			t.Errorf("current deal %s has a final-stage event in the store", deal)
		}
	}
	assertNoQuoteStatusOfCurrentDeals(t, env.DB, current)
	assertNoFutureContactsOrAccountRecords(t, env.DB, sample, cut)
	assertNoStoredPayloadDateAtOrAfter(t, env.DB, cut)
	if n := countRows(t, env.DB, `SELECT count(*) FROM activities`); n == 0 {
		t.Fatal("nothing was ingested")
	}
	assertStateFromBeforeTheCutoff(t, ctx, env, cut)
}

// assertNoQuoteStatusOfCurrentDeals: a quote's final Status is dated at its deal's last event, so no
// status event naming a current deal may exist.
func assertNoQuoteStatusOfCurrentDeals(t *testing.T, db *sql.DB, current map[string]bool) {
	t.Helper()
	rows, err := db.Query(`SELECT source_object_id, payload->>'opportunity_record_id' FROM source_events
 WHERE source_object_id LIKE 'quote:%' AND source_event_key LIKE 'field:Status:%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var quote string
		var opp sql.NullString
		if err := rows.Scan(&quote, &opp); err != nil {
			t.Fatal(err)
		}
		if current[strings.TrimPrefix(opp.String, "opp:")] {
			t.Errorf("%s has its final status in the store, on current deal %s", quote, opp.String)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// assertStateFromBeforeTheCutoff recomputes every account's state and checks that nothing folded into
// it, and no evidence it cites, is dated at or after the cutoff.
func assertStateFromBeforeTheCutoff(t *testing.T, ctx context.Context, env *storetest.Env, cut time.Time) {
	t.Helper()
	// A clock a day ahead makes every debounced job due.
	svc, err := coalesce.New(env.DB, coalesce.Options{Clock: clock.NewFixed(time.Now().Add(24 * time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := svc.Drain(ctx); err != nil || len(res.Recomputes) == 0 {
		t.Fatalf("drain: %d recomputes, %v", len(res.Recomputes), err)
	}
	rows, err := env.DB.Query(`SELECT account_id::text, as_of, state::text FROM account_state`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	states := 0
	for rows.Next() {
		var account, state string
		var asOf time.Time
		if err := rows.Scan(&account, &asOf, &state); err != nil {
			t.Fatal(err)
		}
		states++
		if !asOf.Before(cut) {
			t.Errorf("account %s state is as of %s, at or after the cutoff", account, asOf)
		}
		var doc any
		if err := json.Unmarshal([]byte(state), &doc); err != nil {
			t.Fatal(err)
		}
		assertEvidenceBefore(t, account, doc, cut)
	}
	if err := rows.Err(); err != nil || states == 0 {
		t.Fatalf("account states: %d, %v", states, err)
	}
}

// assertEvidenceBefore walks a state document: every occurred_at, as_of and activity it names is dated
// before the cutoff.
func assertEvidenceBefore(t *testing.T, account string, v any, cut time.Time) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if s, ok := child.(string); ok && (k == "occurred_at" || k == "as_of" || k == "due_at") {
				if at, err := time.Parse(time.RFC3339Nano, s); err == nil && !at.Before(cut) {
					t.Errorf("account %s state carries %s = %s, at or after the cutoff", account, k, s)
				}
			}
			assertEvidenceBefore(t, account, child, cut)
		}
	case []any:
		for _, child := range x {
			assertEvidenceBefore(t, account, child, cut)
		}
	}
}
