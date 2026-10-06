package stageevents

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
	"github.com/harneet2512/gtm-work/core-go/internal/surfacemsg"
)

// cliffWorld is a seeded episode whose run is the run of a Play, and whose BI update is the Play's.
type cliffWorld struct {
	seed     strategytest.Seeded
	manifest string
	account  string
}

func newCliffWorld(t *testing.T) cliffWorld {
	t.Helper()
	world := ctxfixture.Get(t, env.DB)
	w := cliffWorld{seed: strategytest.Seed(t, env.DB, world.AccountA), account: world.AccountA}
	ctxfixture.MarkRunAsPlay(t, env.DB, w.seed.RunID)
	if err := env.DB.QueryRow(`SELECT manifest_id::text FROM demo_plays WHERE activity_id = (SELECT trigger_activity_ids[1] FROM agent_runs WHERE id = $1::uuid)`,
		w.seed.RunID).Scan(&w.manifest); err != nil {
		t.Fatal(err)
	}
	// the Play's own change is the seeded BI update's change: it is what makes message 1 the Play's
	if _, err := env.DB.Exec(`UPDATE demo_plays SET status = 'complete', completed_at = now(),
 account_change_id = (SELECT account_change_id FROM business_intelligence_updates WHERE id = $2::uuid) WHERE manifest_id = $1::uuid`, w.manifest, w.seed.BIID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.DB.Exec(`DELETE FROM pipeline_stage_events WHERE account_id = $1::uuid`, w.account)
		_, _ = env.DB.Exec(`DELETE FROM surface_messages WHERE subject_id IN ($1::uuid, $2::uuid)`, w.seed.BIID, w.seed.EpisodeID)
		_, _ = env.DB.Exec(`DELETE FROM demo_plays WHERE manifest_id = $1::uuid`, w.manifest)
	})
	return w
}

func (w cliffWorld) cliff(t *testing.T) StageDoc {
	t.Helper()
	p, err := newReader(t).Manifest(bg, w.manifest)
	if err != nil {
		t.Fatal(err)
	}
	validDoc(t, p)
	return stageDoc(p, Cliff)
}

func observed(t *testing.T) (ObservedSurfaceMessages, *surfacemsg.Store) {
	t.Helper()
	store, err := surfacemsg.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := newRecorder(t)
	return ObservedSurfaceMessages{Inner: store, Rec: rec}, store
}

func TestCliffIsRunningWhileAMessageIsReservedAndCompletedOnceItsPostIsRecorded(t *testing.T) {
	w := newCliffWorld(t)
	obs, _ := observed(t)

	if d := w.cliff(t); d.Status != Waiting {
		t.Fatalf("before any message: %s, want waiting", d.Status)
	}
	if _, created, err := obs.Reserve(bg, w.seed.BIID, "slack", "bi", "C0DEMO"); err != nil || !created {
		t.Fatalf("reserve: %v created=%v", err, created)
	}
	d := w.cliff(t)
	if d.Status != Running || len(d.Refs.SlackRefs) != 1 || d.Refs.SlackRefs[0].TS != nil {
		t.Fatalf("a reserved message: %+v, want running with a ref whose ts is null (it may or may not have been posted)", d)
	}
	if _, err := obs.RecordTS(bg, w.seed.BIID, "slack", "bi", "1759587744.000200"); err != nil {
		t.Fatal(err)
	}
	d = w.cliff(t)
	if d.Status != Completed || d.Refs.SlackRefs[0].TS == nil || *d.Refs.SlackRefs[0].TS != "1759587744.000200" || d.Refs.SlackRefs[0].Channel != "C0DEMO" {
		t.Fatalf("a posted message: %+v", d)
	}
}

func TestCliffAccumulatesTheThreeMessagesOfAnEpisode(t *testing.T) {
	w := newCliffWorld(t)
	obs, _ := observed(t)
	for _, m := range []struct{ subject, kind, ts string }{
		{w.seed.BIID, "bi", "1759587744.000200"},
		{w.seed.EpisodeID, "chooser", "1759587745.000100"},
	} {
		if _, _, err := obs.Reserve(bg, m.subject, "slack", m.kind, "C0DEMO"); err != nil {
			t.Fatal(err)
		}
		if _, err := obs.RecordTS(bg, m.subject, "slack", m.kind, m.ts); err != nil {
			t.Fatal(err)
		}
	}
	d := w.cliff(t)
	if d.Status != Completed || len(d.Refs.SlackRefs) != 2 {
		t.Fatalf("two posted messages: %+v", d)
	}
	// message 3 is reserved later, after the human acted. Completed is final (overall `complete` is never taken
	// back): the new message is listed with a null ts and named in the detail, and the stage does not reopen.
	before := w.cliff(t)
	if _, _, err := obs.Reserve(bg, w.seed.EpisodeID, "slack", "judgment", "C0DEMO"); err != nil {
		t.Fatal(err)
	}
	d = w.cliff(t)
	if d.Status != Completed || len(d.Refs.SlackRefs) != 3 || d.Refs.SlackRefs[2].TS != nil || d.EndedAt == nil || !d.EndedAt.Equal(*before.EndedAt) {
		t.Fatalf("a third message reserved: %+v, want completed (unchanged end) with 3 refs, the last unposted", d)
	}
	if d.Detail == nil || !strings.Contains(*d.Detail, "judgment reserved") {
		t.Errorf("detail = %v, want it to say the judgment post is not recorded yet", d.Detail)
	}
	if _, err := obs.RecordTS(bg, w.seed.EpisodeID, "slack", "judgment", "1759587900.000300"); err != nil {
		t.Fatal(err)
	}
	if d := w.cliff(t); d.Status != Completed || len(d.Refs.SlackRefs) != 3 || d.Refs.SlackRefs[2].TS == nil {
		t.Fatalf("all three posted: %+v", d)
	}
}

func TestARepeatedReservationOrPostChangesNothing(t *testing.T) {
	w := newCliffWorld(t)
	obs, _ := observed(t)
	_, _, _ = obs.Reserve(bg, w.seed.BIID, "slack", "bi", "C0DEMO")
	_, _ = obs.RecordTS(bg, w.seed.BIID, "slack", "bi", "1759587744.000200")
	before := w.cliff(t)

	if _, created, err := obs.Reserve(bg, w.seed.BIID, "slack", "bi", "C0OTHER"); err != nil || created {
		t.Fatalf("a repeated reservation: created=%v err=%v", created, err)
	}
	if _, err := obs.RecordTS(bg, w.seed.BIID, "slack", "bi", "1759587744.000200"); err != nil {
		t.Fatal(err)
	}
	after := w.cliff(t)
	if after.Status != before.Status || len(after.Refs.SlackRefs) != 1 || after.Refs.SlackRefs[0].Channel != "C0DEMO" {
		t.Fatalf("a redelivery changed the stage: %+v -> %+v", before, after)
	}
}

func TestOnlySlackMessagesAndKnownSubjectsAreObservedAndTheInnerCallAlwaysWins(t *testing.T) {
	w := newCliffWorld(t)
	obs, store := observed(t)
	if _, _, err := obs.Reserve(bg, w.seed.BIID, "web", "bi", "C0DEMO"); err != nil {
		t.Fatal(err)
	}
	stranger := "99999999-9999-4999-8999-999999999991"
	ref, created, err := obs.Reserve(bg, stranger, "slack", "bi", "C0DEMO")
	if err != nil || !created || ref.SubjectID != stranger {
		t.Fatalf("a subject no run owns must still be reserved: %+v %v %v", ref, created, err)
	}
	t.Cleanup(func() { _, _ = env.DB.Exec(`DELETE FROM surface_messages WHERE subject_id = $1::uuid`, stranger) })
	if d := w.cliff(t); d.Status != Waiting {
		t.Fatalf("a web message or an unknown subject moved cliff: %+v", d)
	}
	if got, err := obs.Get(bg, stranger, "slack", "bi"); err != nil || got.SubjectID != stranger {
		t.Fatalf("Get must pass through: %v", err)
	}
	// the inner store's errors are returned unchanged
	var conflict *surfacemsg.TSConflictError
	_, _, _ = obs.Reserve(bg, w.seed.EpisodeID, "slack", "chooser", "C0DEMO")
	_, _ = obs.RecordTS(bg, w.seed.EpisodeID, "slack", "chooser", "1759587744.000200")
	if _, err := obs.RecordTS(bg, w.seed.EpisodeID, "slack", "chooser", "1759587999.000900"); !errors.As(err, &conflict) {
		t.Fatalf("a conflicting ts: %v, want *surfacemsg.TSConflictError", err)
	}
	if _, err := store.Get(bg, w.seed.EpisodeID, "slack", "chooser"); err != nil {
		t.Fatal(err)
	}
}

type failingSurface struct{ err error }

func (f failingSurface) Get(context.Context, string, string, string) (surfacemsg.Ref, error) {
	return surfacemsg.Ref{}, f.err
}
func (f failingSurface) Reserve(context.Context, string, string, string, string) (surfacemsg.Ref, bool, error) {
	return surfacemsg.Ref{}, false, f.err
}
func (f failingSurface) RecordTS(context.Context, string, string, string, string) (surfacemsg.Ref, error) {
	return surfacemsg.Ref{}, f.err
}

func TestAFailedSurfaceCallRecordsNothingAndAFailedRecordingNeverFailsTheSurface(t *testing.T) {
	boom := errors.New("db down")
	obs := ObservedSurfaceMessages{Inner: failingSurface{boom}, Rec: nil}
	if _, _, err := obs.Reserve(bg, "x", "slack", "bi", "C"); !errors.Is(err, boom) {
		t.Fatalf("Reserve: %v", err)
	}
	if _, err := obs.RecordTS(bg, "x", "slack", "bi", "1.1"); !errors.Is(err, boom) {
		t.Fatalf("RecordTS: %v", err)
	}
	if _, err := obs.Get(bg, "x", "slack", "bi"); !errors.Is(err, boom) {
		t.Fatalf("Get: %v", err)
	}
}

// A reservation whose post is never recorded (Slack failed, the poster died) must not leave the stage running
// forever: past the Cliff deadline it reads as unknown.
func TestAReservedMessageThatIsNeverPostedGoesUnknown(t *testing.T) {
	w := newCliffWorld(t)
	obs, _ := observed(t)
	if _, _, err := obs.Reserve(bg, w.seed.BIID, "slack", "bi", "C0DEMO"); err != nil {
		t.Fatal(err)
	}
	p, err := newReaderAt(t, time.Now().Add(deadlines[Cliff]+time.Minute)).Manifest(bg, w.manifest)
	if err != nil {
		t.Fatal(err)
	}
	validDoc(t, p)
	d := stageDoc(p, Cliff)
	if d.Status != Unknown || d.FailureKind == nil || *d.FailureKind != Transport || len(d.Refs.SlackRefs) != 1 || d.Refs.SlackRefs[0].TS != nil {
		t.Fatalf("an unposted reservation past its deadline = %+v, want unknown / transport with the ref kept", d)
	}
}
