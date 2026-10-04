package demomine

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/payloadhash"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// freezeOptions are the options to freeze one mined case of the sample at a fixed created_at.
func freezeOptions(rep *Report, top Case) FreezeOptions {
	return FreezeOptions{Dir: filepath.FromSlash(sampleDir), OpportunityID: top.OpportunityID, HeldOutID: top.HeldOut.EventID,
		Report: rep, Now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
}

// TestFreezeTopCase freezes the top mined case once (a replay takes tens of seconds) and checks the manifest
// and the stores from every side the contract cares about.
func TestFreezeTopCase(t *testing.T) {
	rep := cachedSample(t)
	if len(rep.Top) == 0 {
		t.Fatal("the sample has no case to freeze")
	}
	top := rep.Top[0]
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	fr, err := Freeze(context.Background(), env.DB, freezeOptions(&rep, top))
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	t.Run("manifest", func(t *testing.T) { checkManifest(t, top, fr, env.DB) })
	t.Run("leakage", func(t *testing.T) { checkNoLeak(t, top, fr, env.DB) })
	t.Run("reproducible", func(t *testing.T) { checkReproducible(t, &rep, top, fr) })
}

// checkReproducible freezes the same case again in a second, separate database (new database-assigned ids) and
// requires the same manifest id and content hash.
func checkReproducible(t *testing.T, rep *Report, top Case, first Frozen) {
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start the second db: %v", err)
	}
	defer env.Close()
	second, err := Freeze(context.Background(), env.DB, freezeOptions(rep, top))
	if err != nil {
		t.Fatalf("second freeze: %v", err)
	}
	if first.Manifest.AccountID == second.Manifest.AccountID || first.Manifest.OpportunityID == second.Manifest.OpportunityID {
		t.Fatal("setup: the two databases must have assigned different ids, or this test proves nothing")
	}
	if first.Manifest.ID != second.Manifest.ID || first.Manifest.ContentSHA256 != second.Manifest.ContentSHA256 {
		t.Fatalf("two freezes of the same case differ: id %s vs %s, hash %s vs %s",
			first.Manifest.ID, second.Manifest.ID, first.Manifest.ContentSHA256, second.Manifest.ContentSHA256)
	}
	if err := VerifyHash(second.JSON); err != nil {
		t.Fatal(err)
	}
}

func checkManifest(t *testing.T, top Case, fr Frozen, db *sql.DB) {
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("demo_manifest", fr.JSON); err != nil {
		t.Fatalf("manifest does not validate against demo_manifest.v1.json: %v", err)
	}
	if err := VerifyHash(fr.JSON); err != nil {
		t.Fatal(err)
	}
	if err := Persist(context.Background(), db, fr.Manifest); err != nil {
		t.Fatalf("the demo_manifests constraints refuse the frozen manifest: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM demo_manifests WHERE content_sha256 = $1 AND held_out_event_id = $2::uuid`,
		fr.Manifest.ContentSHA256, top.HeldOut.EventID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("manifest row: count %d, err %v", n, err)
	}

	// Event N's results are absent: it is bare, it is not among the history, and no history state is past N-1.
	var doc map[string]any
	if err := json.Unmarshal(fr.JSON, &doc); err != nil {
		t.Fatal(err)
	}
	held := doc["held_out_event"].(map[string]any)
	for k := range held {
		switch k {
		case "event_id", "provenance", "occurred_at", "replay_position", "payload_sha256":
		default:
			t.Errorf("held_out_event carries %q: Event N must be bare", k)
		}
	}
	checkPayloadPin(t, top, held)
	if len(fr.Manifest.Events) != len(top.History) {
		t.Fatalf("manifest history has %d events, the case has %d", len(fr.Manifest.Events), len(top.History))
	}
	for i, e := range fr.Manifest.Events {
		if e.Event.EventID == top.HeldOut.EventID {
			t.Fatalf("Event N is in the history at %d", i)
		}
		if e.Event.ReplayPosition != i+1 || e.Event.OccurredAt >= top.HeldOut.OccurredAt {
			t.Errorf("history event %d: position %d at %s, not before Event N (%s)", i+1, e.Event.ReplayPosition, e.Event.OccurredAt, top.HeldOut.OccurredAt)
		}
	}
	if fr.Manifest.HeldOutEvent.ReplayPosition != len(top.History)+1 || fr.Manifest.DataCutoff != top.History[len(top.History)-1].OccurredAt {
		t.Fatalf("held-out position %d / cutoff %s", fr.Manifest.HeldOutEvent.ReplayPosition, fr.Manifest.DataCutoff)
	}
	last := fr.Manifest.Events[len(fr.Manifest.Events)-1].StateAfter
	var maxVersion int
	if err := db.QueryRow(`SELECT max(version) FROM opportunity_state_history WHERE opportunity_id = $1::uuid`, fr.Manifest.OpportunityID).Scan(&maxVersion); err != nil {
		t.Fatal(err)
	}
	if last.OpportunityID == nil || last.Version != maxVersion {
		t.Fatalf("the deal's latest state version is %d, the manifest's last snapshot is %+v: something past N-1 was written", maxVersion, last)
	}
	if e := fr.Manifest.SelectionExpectations; e == nil || len(e.MaterialDimensions) == 0 {
		t.Fatalf("expectations missing for a case from the report: %+v", e)
	}
}

// Leakage: after N-1 the stores hold nothing from Event N or later.
func checkNoLeak(t *testing.T, top Case, fr Frozen, db *sql.DB) {
	held := top.HeldOut
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM source_events WHERE source_system = $1 AND source_object_id = $2 AND source_event_key = $3`,
		held.SourceSystem, held.SourceObjectID, held.SourceEventKey).Scan(&n); err != nil || n != 0 {
		t.Fatalf("Event N is in source_events (%d, %v)", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM activities WHERE opportunity_id = $1::uuid AND occurred_at > $2::timestamptz`,
		fr.Manifest.OpportunityID, fr.Manifest.DataCutoff).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d activities of the deal are dated after the data cutoff (%v)", n, err)
	}
	cutoff, _ := time.Parse(time.RFC3339, held.OccurredAt)
	dealCutoff, _ := time.Parse(time.RFC3339, fr.Manifest.DataCutoff)
	for _, q := range []struct{ sql, id string }{
		{`SELECT state::text FROM opportunity_state WHERE opportunity_id = $1::uuid`, fr.Manifest.OpportunityID},
		{`SELECT state::text FROM account_state WHERE account_id = $1::uuid`, fr.Manifest.AccountID},
	} {
		var raw string
		if err := db.QueryRow(q.sql, q.id).Scan(&raw); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
		if strings.Contains(raw, held.SourceObjectID) {
			t.Errorf("the state mentions Event N's source object %s", held.SourceObjectID)
		}
		var doc any
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		if at := maxTime(doc, ""); at.After(cutoff) {
			t.Errorf("state holds a time (%s) after Event N (%s)", at.Format(time.RFC3339), cutoff.Format(time.RFC3339))
		}
		// The evidence the deal state cites happened no later than the data cutoff.
		if strings.Contains(q.sql, "opportunity_state") {
			if at := maxTime(doc, "occurred_at"); at.After(dealCutoff) {
				t.Errorf("the deal state cites evidence dated %s, after the data cutoff %s", at.Format(time.RFC3339), dealCutoff.Format(time.RFC3339))
			}
		}
	}
}

// maxTime is the latest RFC 3339 string in the document, under key when key is not empty.
func maxTime(doc any, key string) time.Time {
	var worst time.Time
	var walk func(v any, k string)
	walk = func(v any, k string) {
		switch x := v.(type) {
		case map[string]any:
			for name, c := range x {
				walk(c, name)
			}
		case []any:
			for _, c := range x {
				walk(c, k)
			}
		case string:
			if key != "" && k != key {
				return
			}
			if at, err := time.Parse(time.RFC3339, x); err == nil && at.After(worst) {
				worst = at
			}
		}
	}
	walk(doc, "")
	return worst
}

func TestFreezeRefusesBadSelections(t *testing.T) {
	rep := cachedSample(t)
	top := rep.Top[0]
	// Selection errors surface before any database work, so no database is needed.
	for name, id := range map[string]string{
		"unknown held-out event":      "00000000-0000-5000-8000-000000000000",
		"held-out is the first event": top.History[0].EventID,
	} {
		o := freezeOptions(&rep, top)
		o.HeldOutID = id
		if _, err := Freeze(context.Background(), nil, o); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}

	// A report whose history disagrees with the replay is refused; the matching one is accepted.
	m := Manifest{HeldOutEvent: HeldOutEvent{EventID: top.HeldOut.EventID}}
	for _, e := range top.History {
		m.Events = append(m.Events, ManifestEvent{Event: HeldOutEvent{EventID: e.EventID}, MaterialDimensions: e.Dimensions})
	}
	o := freezeOptions(&rep, top)
	if _, err := applySelection(m, o); err != nil {
		t.Fatalf("a matching report was refused: %v", err)
	}
	stale := rep
	c := top
	c.History = append([]EventRecord{}, top.History...)
	c.History[0].Dimensions = []string{DimAction}
	stale.Top = []Case{c}
	o.Report = &stale
	if _, err := applySelection(m, o); err == nil {
		t.Fatal("a stale report must be refused")
	}
	// A case outside the report needs an explicit reason.
	o.Report, o.Why = nil, ""
	if _, err := applySelection(m, o); err == nil {
		t.Fatal("a case without a report needs --why")
	}
}

// checkPayloadPin: event N pins the payload the replay dataset must hold for it (what Play verifies).
func checkPayloadPin(t *testing.T, top Case, held map[string]any) {
	t.Helper()
	snap, err := crmarena.Load(filepath.FromSlash(sampleDir))
	if err != nil {
		t.Fatal(err)
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Events {
		if EventUUID(e.Source.SourceSystem, e.Source.SourceObjectID, e.Source.SourceEventKey) != top.HeldOut.EventID {
			continue
		}
		known, _, err := e.AsKnown()
		if err != nil {
			t.Fatal(err)
		}
		want, err := payloadhash.SHA256(known.Source.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := held["payload_sha256"].(string); got != want {
			t.Fatalf("held_out_event.payload_sha256 = %q, want %q (the as-known payload of Event N)", got, want)
		}
		return
	}
	t.Fatalf("Event N %s is not in the sample", top.HeldOut.EventID)
}
