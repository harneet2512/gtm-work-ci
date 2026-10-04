package play

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
)

// manifestForEvents writes a manifest whose history events sit at the given positions; the held-out event is
// always at len(positions)+1 (the CHECK on demo_manifests requires it). occurredAt offsets the event times so
// the sequence can prove it normalizes them.
func manifestForEvents(t *testing.T, world replaytest.World, positions []int, occurredAt string) string {
	t.Helper()
	events := make([]map[string]any, 0, len(positions))
	for i, p := range positions {
		events = append(events, map[string]any{"event": map[string]any{
			"event_id":        replaytest.HistoryEventID(i + 1),
			"provenance":      map[string]any{"origin": "dataset", "provenance": "crmarena-pro:b2b", "source": map[string]any{"source_system": "email", "source_object_id": fmt.Sprintf("<m%d@x.com>", i+1)}, "source_event_key": "received"},
			"occurred_at":     occurredAt,
			"replay_position": p,
		}})
	}
	held := map[string]any{
		"event_id":        "0e7e0000-0000-4000-8000-000000000099",
		"provenance":      map[string]any{"origin": "dataset", "provenance": "crmarena-pro:b2b", "source": map[string]any{"source_system": "email", "source_object_id": "<held@x.com>"}, "source_event_key": "received"},
		"occurred_at":     occurredAt,
		"replay_position": positions[len(positions)-1] + 1, // demo_manifests_held_out_is_next
		"payload_sha256":  strings.Repeat("b", 64),
	}
	eventsRaw, _ := json.Marshal(events)
	heldRaw, _ := json.Marshal(held)
	return replaytest.One(t, env.DB, `INSERT INTO demo_manifests (account_id, opportunity_id, data_cutoff, events, held_out_event, why_selected, content_sha256)
		VALUES ($1::uuid, $2::uuid, now(), $3::jsonb, $4::jsonb, 'sequence test', repeat('a', 64)) RETURNING id::text`,
		world.Account, world.Opportunity, string(eventsRaw), string(heldRaw))
}

func TestTheSequenceIsTheManifestEventsInReplayOrder(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	seq, err := LoadSequence(bg, env.DB, r.manifest)
	if err != nil {
		t.Fatalf("LoadSequence: %v", err)
	}
	if seq.ManifestID != r.manifest || seq.AccountID != r.world.Account || seq.OpportunityID != r.world.Opportunity {
		t.Fatalf("sequence identity: %+v", seq)
	}
	if seq.N() != 3 {
		t.Fatalf("N() = %d, want 3", seq.N())
	}
	for i, ev := range seq.Events {
		if ev.Position != i+1 {
			t.Fatalf("position at index %d = %d, want %d", i, ev.Position, i+1)
		}
	}
	if seq.Events[0].EventID != replaytest.HistoryEventID(1) || seq.Events[1].EventID != replaytest.HistoryEventID(2) {
		t.Fatalf("history ids: %s, %s", seq.Events[0].EventID, seq.Events[1].EventID)
	}
	if seq.Events[0].Source.System != "email" || seq.Events[0].Source.ObjectID != "<m1@x.com>" || seq.Events[0].Source.EventKey != "received" {
		t.Fatalf("source ref of event 1: %+v", seq.Events[0].Source)
	}
}

func TestTheHeldOutEventIsTheLastEventOfTheSequence(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	seq, err := LoadSequence(bg, env.DB, r.manifest)
	if err != nil {
		t.Fatalf("LoadSequence: %v", err)
	}
	last, ok := seq.At(seq.N())
	if !ok || !last.HeldOut {
		t.Fatalf("At(N) = %+v ok=%v", last, ok)
	}
	if last.EventID != r.held.EventID {
		t.Fatalf("held-out event id = %s, want %s", last.EventID, r.held.EventID)
	}
	if last.PayloadSHA256 == "" {
		t.Error("the held-out event carries its payload pin")
	}
	for k := 1; k < seq.N(); k++ {
		ev, _ := seq.At(k)
		if ev.HeldOut || ev.PayloadSHA256 != "" {
			t.Fatalf("history event %d is marked held-out", k)
		}
	}
	if _, ok := seq.At(seq.N() + 1); ok {
		t.Error("At(N+1) must not exist")
	}
	if _, ok := seq.At(0); ok {
		t.Error("At(0) must not exist")
	}
}

func TestASequenceWithAGapIsRefused(t *testing.T) {
	world := replaytest.SeedWorld(t, env.DB)
	id := manifestForEvents(t, world, []int{1, 2, 4}, "2026-09-29T10:00:00Z") // held-out lands on 4 too
	_, err := LoadSequence(bg, env.DB, id)
	if !errors.Is(err, ErrSequenceInvalid) {
		t.Fatalf("err = %v, want ErrSequenceInvalid", err)
	}
}

func TestASequenceWithADuplicateIsRefused(t *testing.T) {
	world := replaytest.SeedWorld(t, env.DB)
	id := manifestForEvents(t, world, []int{1, 1}, "2026-09-29T10:00:00Z")
	_, err := LoadSequence(bg, env.DB, id)
	if !errors.Is(err, ErrSequenceInvalid) {
		t.Fatalf("err = %v, want ErrSequenceInvalid", err)
	}
}

func TestSequenceTimesAreNormalizedToUTC(t *testing.T) {
	world := replaytest.SeedWorld(t, env.DB)
	id := manifestForEvents(t, world, []int{1}, "2026-09-29T10:00:00+02:00")
	seq, err := LoadSequence(bg, env.DB, id)
	if err != nil {
		t.Fatalf("LoadSequence: %v", err)
	}
	for i, ev := range seq.Events {
		if ev.OccurredAt.Location() != time.UTC || ev.OccurredAt.Hour() != 8 {
			t.Fatalf("event %d occurred_at = %s, want 08:00 UTC", i+1, ev.OccurredAt.Format(time.RFC3339))
		}
	}
	if seq.DataCutoff.Location() != time.UTC {
		t.Fatalf("data_cutoff = %s", seq.DataCutoff.Format(time.RFC3339))
	}
}

func TestAMissingManifestIsNotFound(t *testing.T) {
	for _, id := range []string{"00000000-0000-4000-8000-000000000000", "not-a-uuid"} {
		if _, err := LoadSequence(bg, env.DB, id); !errors.Is(err, ErrManifestNotFound) {
			t.Fatalf("LoadSequence(%q): err = %v, want ErrManifestNotFound", id, err)
		}
	}
}
