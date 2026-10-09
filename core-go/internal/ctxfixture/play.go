package ctxfixture

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// MarkRunAsPlay makes the run's first trigger activity the activity of a COMPLETED demo play (a manifest, a demo_plays
// row and the account change and update the play wrote), which is what makes it "the play's run" to the run driver's
// play scope: the driver waits for the play to complete so the run's world read sees its account change. Test use only.
func MarkRunAsPlay(t testing.TB, db *sql.DB, runID string) {
	t.Helper()
	manifest, event, account, activity := insertPlay(t, db, runID)
	completePlay(t, db, runID, manifest, account, event)
	_ = activity
}

// MarkRunAsPlayInFlight is MarkRunAsPlay for a play that has been released but has not completed yet (no account
// change): its run must not be driven until CompletePlay.
func MarkRunAsPlayInFlight(t testing.TB, db *sql.DB, runID string) (manifestID string) {
	t.Helper()
	manifest, _, _, _ := insertPlay(t, db, runID)
	return manifest
}

// CompletePlay completes a play left in flight by MarkRunAsPlayInFlight.
func CompletePlay(t testing.TB, db *sql.DB, runID, manifestID string) {
	t.Helper()
	var account, event string
	if err := db.QueryRow(`SELECT account_id::text, held_out_event_id::text FROM demo_plays WHERE manifest_id = $1::uuid`, manifestID).Scan(&account, &event); err != nil {
		t.Fatalf("ctxfixture: read play %s: %v", manifestID, err)
	}
	completePlay(t, db, runID, manifestID, account, event)
}

// completePlay writes the account change (and the play's completion) the way the BI writer's transaction does.
func completePlay(t testing.TB, db *sql.DB, runID, manifest, account, event string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO state_diffs (account_id, from_version, to_version, is_material, changes, activity_ids)
 SELECT account_id, 0, max(version), false, '[]', '{}' FROM state_history WHERE account_id = $1::uuid GROUP BY account_id HAVING max(version) > 0
 ON CONFLICT (account_id, to_version) DO NOTHING`, account); err != nil {
		t.Fatalf("ctxfixture: insert the play's state diff: %v", err)
	}
	var change string
	if err := tx.QueryRow(`INSERT INTO account_changes (account_id, held_out_event_id, trigger_activity_ids, previous_state_ref, current_state_ref,
   state_diff_id, graph_diff_ref, material_change, evidence_refs)
 SELECT $1::uuid, $2::uuid, r.trigger_activity_ids, jsonb_build_object('account_id', $1::text, 'version', d.from_version), jsonb_build_object('account_id', $1::text, 'version', d.to_version), d.id, '{}', d.is_material, CASE WHEN d.is_material THEN '[{"fixture":true}]'::jsonb ELSE '[]'::jsonb END
   FROM agent_runs r, LATERAL (SELECT id, is_material, from_version, to_version FROM state_diffs WHERE account_id = $1::uuid ORDER BY to_version DESC LIMIT 1) d
  WHERE r.id = $3::uuid RETURNING id::text`, account, event, runID).Scan(&change); err != nil {
		t.Fatalf("ctxfixture: insert the play's account change (the account needs a state diff): %v", err)
	}
	if _, err := tx.Exec(`UPDATE demo_plays SET status = 'complete', account_change_id = $2::uuid, completed_at = now() WHERE manifest_id = $1::uuid`, manifest, change); err != nil {
		t.Fatalf("ctxfixture: complete the play: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func insertPlay(t testing.TB, db *sql.DB, runID string) (manifest, event, account, activity string) {
	t.Helper()
	if err := db.QueryRow(`SELECT account_id::text, trigger_activity_ids[1]::text, gen_random_uuid()::text FROM agent_runs WHERE id = $1::uuid`, runID).
		Scan(&account, &activity, &event); err != nil {
		t.Fatalf("ctxfixture: read run %s: %v", runID, err)
	}
	stamp := fmt.Sprintf("%064x", time.Now().UnixNano())
	held := fmt.Sprintf(`{"event_id":%q,"replay_position":2,"payload_sha256":%q}`, event, stamp)
	var opp string
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
	return manifest, event, account, activity
}
