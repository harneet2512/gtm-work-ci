package ingest_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
)

// storedOrigin reads the origin markers of one stored source event ("" for NULL).
func storedOrigin(t *testing.T, eventID string) (origin, provenance string) {
	t.Helper()
	var o, p sql.NullString
	if err := env.DB.QueryRow(`SELECT origin, provenance FROM source_events WHERE id = $1`, eventID).Scan(&o, &p); err != nil {
		t.Fatalf("read origin: %v", err)
	}
	return o.String, p.String
}

// WP32 (HAR-131): a synthetic event keeps its markers in source_events (audit), and a redelivery
// of the same identity triple can never relabel the stored event.
func TestIngestStoresOriginMarkersAndNeverRelabels(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})

	syn := inboundEmail(t, "<syn1-reply-1@acme.com>", "marco.ruiz@acme.com", "", t0.Add(-time.Hour))
	syn.Origin, syn.Provenance = "synthetic", "synthetic:v1"
	res := mustIngest(t, svc, syn)
	if o, p := storedOrigin(t, res.SourceEventID); o != "synthetic" || p != "synthetic:v1" {
		t.Fatalf("stored origin %q provenance %q, want synthetic / synthetic:v1", o, p)
	}

	relabel := syn
	relabel.Origin, relabel.Provenance = "live", ""
	again := mustIngest(t, svc, relabel)
	if !again.Duplicate || again.SourceEventID != res.SourceEventID {
		t.Fatalf("redelivery = %+v, want duplicate of %s", again, res.SourceEventID)
	}
	if o, p := storedOrigin(t, res.SourceEventID); o != "synthetic" || p != "synthetic:v1" {
		t.Fatalf("redelivery relabelled the event to %q / %q", o, p)
	}

	plain := mustIngest(t, svc, inboundEmail(t, "m-plain", "marco.ruiz@acme.com", "", t0.Add(-time.Minute)))
	if o, p := storedOrigin(t, plain.SourceEventID); o != "" || p != "" {
		t.Fatalf("undeclared origin stored as %q / %q, want NULL", o, p)
	}
}

func TestIngestRejectsInconsistentOriginMarkers(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})
	ev := inboundEmail(t, "m-bad", "marco.ruiz@acme.com", "", t0.Add(-time.Hour))
	ev.Origin = "synthetic" // no provenance
	if _, err := svc.Ingest(t.Context(), ev); err == nil {
		t.Fatal("synthetic event without provenance accepted")
	}
}
