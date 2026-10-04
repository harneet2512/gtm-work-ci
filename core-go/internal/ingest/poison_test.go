package ingest_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// A parked row whose Prepare or Finalize keeps failing must not abort the ingest that triggered
// re-resolution: it is rolled back on its own, counted as a failed attempt, and the pass goes on.
func TestPoisonedParkedRowDoesNotBlockIngestOrOtherRows(t *testing.T) {
	for _, where := range []string{"prepare", "finalize"} {
		t.Run(where, func(t *testing.T) {
			changed := false
			ext := &fakeExtension{
				prepare: func(ctx context.Context, tx *sql.Tx, ev normalize.SourceEvent, act normalize.Activity) (ingest.Prepared, error) {
					if changed && where == "prepare" && ev.SourceObjectID == "sick" {
						return nil, errors.New("poisoned row")
					}
					return nil, nil
				},
				finalize: func(ctx context.Context, tx *sql.Tx, in ingest.FinalizeInput) (ingest.FinalizeResult, error) {
					if changed && where == "finalize" && in.Event.SourceObjectID == "sick" {
						return ingest.FinalizeResult{}, errors.New("poisoned row")
					}
					return ingest.FinalizeResult{EntitiesChanged: changed}, nil
				},
			}
			svc := newExtService(t, ext)
			parkMany(t, 2) // rows that can never resolve
			sick := mustIngest(t, svc, inboundEmail(t, "sick", "sick@late.example", "", t0))
			late := mustIngest(t, svc, inboundEmail(t, "late", "buyer@late.example", "", t0))
			if late.AccountID != nil || sick.AccountID != nil {
				t.Fatal("setup: the late email must be parked first")
			}
			account := seedAccount(t, "Late", "late.example")
			changed = true

			trigger, err := svc.Ingest(context.Background(), inboundEmail(t, "trigger", "x@late.example", "", t0.Add(time.Minute)))

			if err != nil {
				t.Fatalf("a poisoned parked row blocked an unrelated ingest: %v", err)
			}
			if trigger.AccountID == nil || *trigger.AccountID != account {
				t.Errorf("the new event was not ingested onto its account: %v", trigger.AccountID)
			}
			if got := queryString(t, `SELECT account_id::text FROM activities WHERE id = $1::uuid`, late.ActivityID); got != account {
				t.Error("a healthy parked row behind the poisoned one was not re-resolved")
			}
			if got := queryString(t, `SELECT attempts::text FROM unresolved_activities u JOIN activities a ON a.id = u.activity_id WHERE a.id = $1::uuid`, sick.ActivityID); got != "1" {
				t.Errorf("the poisoned row has attempts = %s, want 1", got)
			}
		})
	}
}
