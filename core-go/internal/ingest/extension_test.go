package ingest_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// fakeExtension records the calls the ingest transaction makes and lets a test script them.
type fakeExtension struct {
	prepare  func(ctx context.Context, tx *sql.Tx, ev normalize.SourceEvent, act normalize.Activity) (ingest.Prepared, error)
	finalize func(ctx context.Context, tx *sql.Tx, in ingest.FinalizeInput) (ingest.FinalizeResult, error)
	calls    []string
}

// carried is the Prepared value the fake hands from Prepare to Finalize.
type carried struct{ note string }

func (c *carried) EntitiesChanged() bool { return false }

func (f *fakeExtension) Prepare(ctx context.Context, tx *sql.Tx, ev normalize.SourceEvent, act normalize.Activity) (ingest.Prepared, error) {
	f.calls = append(f.calls, "prepare:"+ev.SourceObjectID)
	if f.prepare == nil {
		return nil, nil
	}
	return f.prepare(ctx, tx, ev, act)
}

func (f *fakeExtension) Finalize(ctx context.Context, tx *sql.Tx, in ingest.FinalizeInput) (ingest.FinalizeResult, error) {
	tag := "finalize:" + in.Event.SourceObjectID
	if in.Reresolved {
		tag += ":re"
	}
	f.calls = append(f.calls, tag)
	if f.finalize == nil {
		return ingest.FinalizeResult{}, nil
	}
	return f.finalize(ctx, tx, in)
}

func newExtService(t *testing.T, ext ingest.Extension) *ingest.Service {
	t.Helper()
	resetDB(t)
	return newService(t, clock.NewFixed(t0), ingest.Options{Extension: ext})
}

func TestPrepareRunsBeforeResolutionAndFinalizeAfterTheActivityExists(t *testing.T) {
	var prepared string
	ext := &fakeExtension{
		prepare: func(ctx context.Context, tx *sql.Tx, ev normalize.SourceEvent, act normalize.Activity) (ingest.Prepared, error) {
			if err := tx.QueryRowContext(ctx, `INSERT INTO accounts (name, domain) VALUES ('Acme', 'acme.com') RETURNING id::text`).Scan(&prepared); err != nil {
				return nil, err
			}
			return &carried{note: "carried"}, nil
		},
		finalize: func(ctx context.Context, tx *sql.Tx, in ingest.FinalizeInput) (ingest.FinalizeResult, error) {
			if c, ok := in.Prepared.(*carried); !ok || c.note != "carried" {
				t.Errorf("Finalize got Prepared = %v, want the value Prepare returned", in.Prepared)
			}
			if in.Resolution.AccountID != prepared {
				t.Errorf("Finalize resolution account = %q, want the account Prepare created (%q)", in.Resolution.AccountID, prepared)
			}
			var parts int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM activity_participants WHERE activity_id = $1::uuid`, in.ActivityID).Scan(&parts); err != nil {
				return ingest.FinalizeResult{}, err
			}
			if parts == 0 {
				t.Error("participants must exist when Finalize runs")
			}
			if in.Activity.Type() != "EmailReceived" || in.Event.SourceObjectID != "m1" {
				t.Errorf("Finalize got activity %s / event %s", in.Activity.Type(), in.Event.SourceObjectID)
			}
			return ingest.FinalizeResult{}, nil
		},
	}
	svc := newExtService(t, ext)

	res := mustIngest(t, svc, inboundEmail(t, "m1", "priya.shah@acme.com", "", t0))

	if res.AccountID == nil || *res.AccountID != prepared {
		t.Fatalf("activity resolved to %v, want the account created in Prepare (%s)", res.AccountID, prepared)
	}
	if got := strings.Join(ext.calls, ","); got != "prepare:m1,finalize:m1" {
		t.Errorf("call order = %s", got)
	}
}

func TestExtensionIsNotCalledForDuplicateDeliveries(t *testing.T) {
	ext := &fakeExtension{}
	svc := newExtService(t, ext)
	ev := inboundEmail(t, "m1", "priya.shah@acme.com", "", t0)

	mustIngest(t, svc, ev)
	mustIngest(t, svc, ev)

	if len(ext.calls) != 2 {
		t.Errorf("calls = %v, want exactly one Prepare and one Finalize for two deliveries", ext.calls)
	}
}

func TestExtensionErrorsRollBackTheWholeEvent(t *testing.T) {
	boom := errors.New("boom")
	for name, ext := range map[string]*fakeExtension{
		"prepare": {prepare: func(context.Context, *sql.Tx, normalize.SourceEvent, normalize.Activity) (ingest.Prepared, error) {
			return nil, boom
		}},
		"finalize": {finalize: func(context.Context, *sql.Tx, ingest.FinalizeInput) (ingest.FinalizeResult, error) {
			return ingest.FinalizeResult{}, boom
		}},
	} {
		t.Run(name, func(t *testing.T) {
			svc := newExtService(t, ext)
			_, err := svc.Ingest(context.Background(), inboundEmail(t, "m1", "priya.shah@acme.com", "", t0))
			if !errors.Is(err, boom) {
				t.Fatalf("error = %v, want it to wrap the extension error", err)
			}
			if n := count(t, "source_events") + count(t, "activities"); n != 0 {
				t.Errorf("%d rows survived a failed extension call", n)
			}
		})
	}
}

// An activity parked as unresolved is rebuilt from its stored source event, re-run through the
// resolver and moved onto the account once the extension reports that entities appeared.
func TestUnresolvedActivitiesAreReresolvedWhenEntitiesAppear(t *testing.T) {
	ext := &fakeExtension{}
	svc := newExtService(t, ext)
	first := mustIngest(t, svc, inboundEmail(t, "m1", "priya.shah@acme.com", "", t0))
	if first.AccountID != nil || count(t, "unresolved_activities") != 1 {
		t.Fatalf("setup: the first email should be unresolved, got %+v", first)
	}
	// Mid-pass link: a person for the email is only created after the account exists.
	var accountID string
	ext.prepare = func(ctx context.Context, tx *sql.Tx, ev normalize.SourceEvent, act normalize.Activity) (ingest.Prepared, error) {
		if ev.SourceObjectID != "trigger" {
			return nil, nil
		}
		return nil, tx.QueryRowContext(ctx, `INSERT INTO accounts (name, domain) VALUES ('Acme', 'acme.com') RETURNING id::text`).Scan(&accountID)
	}
	ext.finalize = func(ctx context.Context, tx *sql.Tx, in ingest.FinalizeInput) (ingest.FinalizeResult, error) {
		return ingest.FinalizeResult{EntitiesChanged: in.Event.SourceObjectID == "trigger"}, nil
	}

	trigger := mustIngest(t, svc, inboundEmail(t, "trigger", "dana2@acme.com", "", t0.Add(time.Hour)))
	if trigger.AccountID == nil {
		t.Fatal("trigger email should resolve to the account created in Prepare")
	}

	if got := queryString(t, `SELECT account_id::text FROM activities WHERE source_object_id = 'm1'`); got != accountID {
		t.Errorf("m1 account = %q, want %q after re-resolution", got, accountID)
	}
	if n := count(t, "unresolved_activities"); n != 0 {
		t.Errorf("%d activities still unresolved", n)
	}
	jobs := queryString(t, `SELECT cardinality(activity_ids)::text FROM recompute_jobs WHERE account_id = $1::uuid`, accountID)
	if jobs != "2" {
		t.Errorf("recompute job covers %s activities, want 2 (trigger + re-resolved m1)", jobs)
	}
	if got := strings.Join(ext.calls, ","); !strings.Contains(got, "prepare:m1") || !strings.Contains(got, "finalize:m1:re") {
		t.Errorf("re-resolution must run Prepare and Finalize(Reresolved) again, calls = %s", got)
	}
}

func TestReresolutionLeavesActivitiesThatStillCannotResolve(t *testing.T) {
	ext := &fakeExtension{finalize: func(context.Context, *sql.Tx, ingest.FinalizeInput) (ingest.FinalizeResult, error) {
		return ingest.FinalizeResult{EntitiesChanged: true}, nil
	}}
	svc := newExtService(t, ext)

	mustIngest(t, svc, inboundEmail(t, "m1", "a@nowhere.example", "", t0))
	mustIngest(t, svc, inboundEmail(t, "m2", "b@nowhere.example", "", t0.Add(time.Minute)))

	if n := count(t, "unresolved_activities"); n != 2 {
		t.Errorf("unresolved = %d, want 2: nothing matches these domains", n)
	}
	if n := count(t, "recompute_jobs"); n != 0 {
		t.Errorf("recompute_jobs = %d, want 0", n)
	}
}
