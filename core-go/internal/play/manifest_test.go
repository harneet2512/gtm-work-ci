package play

import (
	"database/sql"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// A manifest whose events list is empty still loads: Play then has no N-1 cursor and says so instead of failing to
// scan a NULL. (The table's CHECK refuses such a manifest, so the test lifts it inside a transaction that is
// rolled back.)
func TestAManifestWithNoHistoryEventsLoads(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		var name string
		if err := tx.QueryRow(`SELECT conname FROM pg_constraint WHERE conrelid = 'demo_manifests'::regclass
			AND pg_get_constraintdef(oid) LIKE '%jsonb_array_length(events)%'`).Scan(&name); err != nil {
			t.Fatalf("find the history constraint: %v", err)
		}
		for _, stmt := range []string{
			`ALTER TABLE demo_manifests DROP CONSTRAINT ` + name,
			`ALTER TABLE demo_manifests DROP CONSTRAINT demo_manifests_held_out_is_next`,
			`UPDATE demo_manifests SET events = '[]'::jsonb WHERE id = '` + r.manifest + `'::uuid`,
		} {
			if _, err := tx.Exec(stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		m, err := LoadManifest(bg, tx, r.manifest)
		if err != nil {
			t.Fatalf("a manifest with no history events must load: %v", err)
		}
		if m.LastEventID != "" || m.LastPosition != 0 || m.LastSource != (SourceRef{}) {
			t.Errorf("no history: last event = %q at %d (%+v)", m.LastEventID, m.LastPosition, m.LastSource)
		}
		if m.Held.EventID != r.held.EventID || m.Held.PayloadSHA256 == "" || m.AccountID != r.world.Account {
			t.Errorf("the held-out event and account still load: %+v", m)
		}
	})
}

func TestLoadManifestReadsTheCursorAndThePin(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	m, err := LoadManifest(bg, env.DB, r.manifest)
	if err != nil {
		t.Fatal(err)
	}
	last := replaytest.HistoryEvents()[1]
	if m.LastEventID != replaytest.HistoryEventID(2) || m.LastPosition != 2 || m.LastSource != refOf(last) {
		t.Errorf("last event = %q at %d from %+v", m.LastEventID, m.LastPosition, m.LastSource)
	}
	if m.Held.Position != 3 || m.Held.PayloadSHA256 != replaytest.PayloadSHA256(r.held.Event) {
		t.Errorf("held-out event = %+v", m.Held)
	}
}
