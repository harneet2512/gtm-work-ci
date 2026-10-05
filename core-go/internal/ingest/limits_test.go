package ingest_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

const (
	sqlstateLockNotAvailable = "55P03"
	sqlstateQueryCanceled    = "57014"
)

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// sleepResolver runs a slow statement inside the ingest transaction.
type sleepResolver struct{ seconds string }

func (s sleepResolver) Resolve(ctx context.Context, q ingest.Querier, _ normalize.Activity) (ingest.Resolution, error) {
	var out string
	err := q.QueryRowContext(ctx, `SELECT pg_sleep(`+s.seconds+`)::text`).Scan(&out)
	return ingest.Resolution{}, err
}

func TestIngestTransactionHasAStatementTimeout(t *testing.T) {
	resetDB(t)
	svc := newService(t, clock.NewFixed(t0), ingest.Options{
		Resolver:         sleepResolver{seconds: "5"},
		StatementTimeout: 200 * time.Millisecond,
	})
	start := time.Now()
	_, err := svc.Ingest(context.Background(), inboundEmail(t, "m1", "marco@acme.com", "", t0))
	if pgCode(err) != sqlstateQueryCanceled {
		t.Fatalf("err = %v (code %q), want a statement_timeout cancel", err, pgCode(err))
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("statement timeout took %v", d)
	}
	if got := count(t, "source_events"); got != 0 {
		t.Errorf("timed-out ingest left %d source events", got)
	}
}

func TestIngestTransactionHasALockTimeout(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{LockTimeout: 200 * time.Millisecond})
	mustIngest(t, svc, inboundEmail(t, "m1", "marco@acme.com", "", t0)) // creates the pending job

	// Another transaction holds the account's pending job row.
	blocker, err := env.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, err := blocker.Exec(`SELECT 1 FROM recompute_jobs WHERE claimed_at IS NULL FOR UPDATE`); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = svc.Ingest(context.Background(), inboundEmail(t, "m2", "marco@acme.com", "", t0))
	if pgCode(err) != sqlstateLockNotAvailable {
		t.Fatalf("err = %v (code %q), want lock_not_available", err, pgCode(err))
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("lock timeout took %v", d)
	}
	if got := count(t, "source_events"); got != 1 {
		t.Errorf("source_events = %d, want only the first event", got)
	}
}

func TestNewServiceRejectsNegativeTimeouts(t *testing.T) {
	if _, err := ingest.NewService(env.DB, ingest.Options{StatementTimeout: -time.Second}); err == nil {
		t.Error("negative statement timeout accepted")
	}
	if _, err := ingest.NewService(env.DB, ingest.Options{LockTimeout: -time.Second}); err == nil {
		t.Error("negative lock timeout accepted")
	}
}

func TestDuplicateWithDifferentPayloadIsLoggedAtDebug(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := newService(t, clock.NewFixed(t0), ingest.Options{Logger: logger})

	original := inboundEmail(t, "m1", "marco@acme.com", "", t0)
	mustIngest(t, svc, original)

	mustIngest(t, svc, original) // identical redelivery
	if strings.Contains(logs.String(), "differs") {
		t.Fatalf("identical redelivery was reported as differing: %s", logs.String())
	}

	changed := inboundEmail(t, "m1", "marco@acme.com", "", t0)
	changed.Payload = []byte(strings.Replace(string(changed.Payload), "Body of m1", "A different body", 1))
	res := mustIngest(t, svc, changed)

	if !res.Duplicate {
		t.Fatal("same identity triple must still be a duplicate")
	}
	out := logs.String()
	for _, want := range []string{"level=DEBUG", "differs", "source_object_id=m1", "source_event_key=received"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "A different body") || strings.Contains(out, "Body of m1") {
		t.Errorf("payload content must not be logged: %s", out)
	}
	// Stored payload is still the first one.
	if got := queryString(t, `SELECT payload->>'body_text' FROM source_events`); got != "Body of m1" {
		t.Errorf("stored payload was overwritten: %q", got)
	}
}
