package ctxfixture

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// MarkRunAsPlay makes the run's first trigger activity the activity of a demo play (a manifest and a demo_plays
// row), which is what makes it "the play's run" to the run driver's play scope. Test use only.
func MarkRunAsPlay(t testing.TB, db *sql.DB, runID string) {
	t.Helper()
	var account, activity, event string
	if err := db.QueryRow(`SELECT account_id::text, trigger_activity_ids[1]::text, gen_random_uuid()::text FROM agent_runs WHERE id = $1::uuid`, runID).
		Scan(&account, &activity, &event); err != nil {
		t.Fatalf("ctxfixture: read run %s: %v", runID, err)
	}
	stamp := fmt.Sprintf("%064x", time.Now().UnixNano())
	held := fmt.Sprintf(`{"event_id":%q,"replay_position":2,"payload_sha256":%q}`, event, stamp)
	var opp, manifest string
	if err := db.QueryRow(`INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, 'play ' || $2::text, 'renewal') RETURNING id::text`,
		account, stamp).Scan(&opp); err != nil {
		t.Fatalf("ctxfixture: insert opportunity: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO demo_manifests (account_id, opportunity_id, data_cutoff, events, held_out_event, why_selected, content_sha256)
 VALUES ($1::uuid, $2::uuid, now(), '[{"event":{"event_id":"e7e00000-0000-4000-8000-000000000001","replay_position":1}}]', $3::jsonb, 'fixture', $4)
 RETURNING id::text`, account, opp, held, stamp).Scan(&manifest); err != nil {
		t.Fatalf("ctxfixture: insert manifest: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO demo_plays (manifest_id, held_out_event_id, account_id, activity_id) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid)`,
		manifest, event, account, activity); err != nil {
		t.Fatalf("ctxfixture: insert play: %v", err)
	}
}
