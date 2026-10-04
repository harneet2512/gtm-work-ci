package play

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/biwriter"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/payloadhash"
)

// dataset fetches event N from the replay dataset and checks it is the event the manifest describes.
func (s *Service) dataset(ctx context.Context, m Manifest) (normalize.SourceEvent, error) {
	if s.events == nil {
		return normalize.SourceEvent{}, fmt.Errorf("%w: GHOST_REPLAY_EVENTS is not set", ErrSourceUnavailable)
	}
	ev, err := s.events.Lookup(ctx, m.Held.Source)
	if err != nil {
		return normalize.SourceEvent{}, fmt.Errorf("%w: %v", ErrSourceUnavailable, err)
	}
	if ev.OccurredAt == nil || !ev.OccurredAt.UTC().Equal(m.Held.OccurredAt) {
		return normalize.SourceEvent{}, fmt.Errorf("%w: the dataset's event occurred at %v, the manifest says %s", ErrReleaseMismatch, ev.OccurredAt, m.Held.OccurredAt.Format(time.RFC3339))
	}
	for _, p := range [][3]string{{"origin", ev.Origin, m.Held.Origin}, {"provenance", ev.Provenance, m.Held.Provenance}} {
		if p[1] != "" && p[1] != p[2] {
			return normalize.SourceEvent{}, fmt.Errorf("%w: the dataset's event has %s %q, the manifest says %q", ErrReleaseMismatch, p[0], p[1], p[2])
		}
	}
	if err := checkPayloadPin(m.Held.PayloadSHA256, ev.Payload); err != nil {
		return normalize.SourceEvent{}, err
	}
	ev.Origin, ev.Provenance = m.Held.Origin, m.Held.Provenance
	return ev, nil
}

// checkPayloadPin verifies the dataset's payload against the digest the manifest pinned when the case was frozen.
// A manifest with no pin is refused too: nothing would say the payload is the one the case was mined with.
func checkPayloadPin(pin string, payload []byte) error {
	if pin == "" {
		return fmt.Errorf("%w: the manifest pins no payload hash for the held-out event (freeze the case again)", ErrReleaseMismatch)
	}
	got, err := payloadhash.SHA256(payload)
	if err != nil {
		return fmt.Errorf("%w: the dataset's payload cannot be hashed: %v", ErrReleaseMismatch, err)
	}
	if got != pin {
		return fmt.Errorf("%w: the dataset's payload hashes to %s, the manifest pinned %s", ErrReleaseMismatch, got, pin)
	}
	return nil
}

func (s *Service) assertInvisibleAndRecord(ctx context.Context, m Manifest) error {
	rep, err := s.checker.Check(ctx, m)
	if err != nil {
		return err
	}
	if rep.Status == StatusLeaked {
		return &VisibleError{Report: rep}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO demo_plays (manifest_id, held_out_event_id, account_id) VALUES ($1::uuid, $2::uuid, $3::uuid)
 ON CONFLICT (manifest_id) DO NOTHING`, m.ID, m.Held.EventID, m.AccountID); err != nil {
		return fmt.Errorf("play: record the release of %s: %w", m.Held.EventID, err)
	}
	return nil
}

// release ingests event N through the normal path (idempotent: a redelivery finds the stored event) and
// remembers what it became.
func (s *Service) release(ctx context.Context, m Manifest, ev normalize.SourceEvent) (biwriter.Source, error) {
	res, err := s.ingest.Ingest(ctx, ev)
	if err != nil {
		return biwriter.Source{}, fmt.Errorf("play: ingest event %s: %w", m.Held.EventID, err)
	}
	if res.AccountID == nil || *res.AccountID != m.AccountID {
		got := "no account"
		if res.AccountID != nil {
			got = "account " + *res.AccountID
		}
		return biwriter.Source{}, fmt.Errorf("%w: the pipeline attributed it to %s, the manifest's account is %s", ErrReleaseMismatch, got, m.AccountID)
	}
	deal, err := s.activityDeal(ctx, res.ActivityID, m)
	if err != nil {
		return biwriter.Source{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE demo_plays SET source_event_id = $2::uuid, activity_id = $3::uuid WHERE manifest_id = $1::uuid`,
		m.ID, res.SourceEventID, res.ActivityID); err != nil {
		return biwriter.Source{}, fmt.Errorf("play: record the released activity: %w", err)
	}
	return biwriter.Source{ActivityID: res.ActivityID, SourceEventID: res.SourceEventID, OpportunityID: deal, HeldOutEventID: m.Held.EventID}, nil
}

// activityDeal reads the deal the pipeline attributed the released activity to. A different deal than the
// manifest's is a mismatch; an activity attributed to no deal stays account-level (the change and the update then
// carry no deal) rather than being assigned the manifest's, which nothing verified.
func (s *Service) activityDeal(ctx context.Context, activityID string, m Manifest) (string, error) {
	var deal sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT opportunity_id::text FROM activities WHERE id = $1::uuid`, activityID).Scan(&deal)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("play: the released activity %s does not exist", activityID)
	}
	if err != nil {
		return "", fmt.Errorf("play: read the deal of the released activity %s: %w", activityID, err)
	}
	if deal.Valid && deal.String != m.OpportunityID {
		return "", fmt.Errorf("%w: the pipeline attributed it to deal %s, the manifest's deal is %s", ErrReleaseMismatch, deal.String, m.OpportunityID)
	}
	return deal.String, nil
}
