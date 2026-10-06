package surfacemsg_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/surfacemsg"
)

var env *storetest.Env

func TestMain(m *testing.M) { os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e })) }

var bg = context.Background()

func store(t *testing.T) *surfacemsg.Store {
	t.Helper()
	s, err := surfacemsg.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newSubject(t *testing.T) string {
	t.Helper()
	var id string
	if err := env.DB.QueryRow(`SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNewRequiresADatabase(t *testing.T) {
	if _, err := surfacemsg.New(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}

func TestAReservationIsCreatedOnceAndLaterCallersGetTheSameRow(t *testing.T) {
	subject := newSubject(t)
	first, created, err := store(t).Reserve(bg, subject, "slack", "chooser", "C1")
	if err != nil || !created || first.TS != nil || first.Channel != "C1" || first.SubjectID != subject {
		t.Fatalf("first = %+v created %v err %v", first, created, err)
	}
	again, created, err := store(t).Reserve(bg, subject, "slack", "chooser", "C-OTHER")
	if err != nil || created || again.Channel != "C1" || !again.ReservedAt.Equal(first.ReservedAt) {
		t.Fatalf("second = %+v created %v err %v: the first reservation must win and keep its channel and time", again, created, err)
	}
	if _, created, _ := store(t).Reserve(bg, subject, "slack", "judgment", "C1"); !created {
		t.Fatal("another kind of the same subject is its own slot")
	}
	if _, created, _ := store(t).Reserve(bg, subject, "web", "chooser", "C1"); !created {
		t.Fatal("another surface of the same subject is its own slot")
	}
}

func TestConcurrentReservationsHaveExactlyOneOwner(t *testing.T) {
	subject := newSubject(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	owners := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, created, err := store(t).Reserve(bg, subject, "slack", "chooser", "C1")
			if err != nil {
				t.Error(err)
				return
			}
			if created {
				mu.Lock()
				owners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if owners != 1 {
		t.Fatalf("%d callers believe they own the post, want exactly 1", owners)
	}
}

func TestTheTsIsWrittenOnceAndTheSameTsAgainIsANoOp(t *testing.T) {
	subject := newSubject(t)
	if _, _, err := store(t).Reserve(bg, subject, "slack", "chooser", "C1"); err != nil {
		t.Fatal(err)
	}
	ref, err := store(t).RecordTS(bg, subject, "slack", "chooser", "1759587744.000200")
	if err != nil || ref.TS == nil || *ref.TS != "1759587744.000200" {
		t.Fatalf("record = %+v, %v", ref, err)
	}
	if again, err := store(t).RecordTS(bg, subject, "slack", "chooser", "1759587744.000200"); err != nil || *again.TS != *ref.TS {
		t.Fatalf("the same ts again = %+v, %v", again, err)
	}
	_, err = store(t).RecordTS(bg, subject, "slack", "chooser", "1759587744.000999")
	var conflict *surfacemsg.TSConflictError
	if !errors.As(err, &conflict) || conflict.Existing.TS == nil || *conflict.Existing.TS != "1759587744.000200" {
		t.Fatalf("a different ts: %v, want a TSConflictError carrying the recorded ts", err)
	}
	got, err := store(t).Get(bg, subject, "slack", "chooser")
	if err != nil || *got.TS != "1759587744.000200" {
		t.Fatalf("get after a refused overwrite = %+v, %v", got, err)
	}
}

func TestMissingSlotsAreNotFound(t *testing.T) {
	subject := newSubject(t)
	if _, err := store(t).Get(bg, subject, "slack", "bi"); !errors.Is(err, surfacemsg.ErrNotFound) {
		t.Fatalf("get: %v", err)
	}
	if _, err := store(t).RecordTS(bg, subject, "slack", "bi", "1.1"); !errors.Is(err, surfacemsg.ErrNotFound) {
		t.Fatalf("record without a reservation: %v", err)
	}
}

func TestInvalidInputIsRefusedWithErrInvalid(t *testing.T) {
	s, subject := store(t), newSubject(t)
	for name, call := range map[string]func() error{
		"bad subject":    func() error { _, _, err := s.Reserve(bg, "nope", "slack", "chooser", "C1"); return err },
		"upper surface":  func() error { _, _, err := s.Reserve(bg, subject, "Slack", "chooser", "C1"); return err },
		"sql in surface": func() error { _, _, err := s.Reserve(bg, subject, "x';--", "chooser", "C1"); return err },
		"unknown kind":   func() error { _, _, err := s.Reserve(bg, subject, "slack", "message2", "C1"); return err },
		"empty channel":  func() error { _, _, err := s.Reserve(bg, subject, "slack", "chooser", ""); return err },
		"long channel": func() error {
			_, _, err := s.Reserve(bg, subject, "slack", "chooser", strings.Repeat("C", 65))
			return err
		},
		"get bad subject": func() error { _, err := s.Get(bg, "nope", "slack", "chooser"); return err },
		"ts not a ts":     func() error { _, err := s.RecordTS(bg, subject, "slack", "chooser", "yesterday"); return err },
		"empty ts":        func() error { _, err := s.RecordTS(bg, subject, "slack", "chooser", ""); return err },
		"record bad kind": func() error { _, err := s.RecordTS(bg, subject, "slack", "x", "1.1"); return err },
	} {
		if err := call(); !errors.Is(err, surfacemsg.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

func TestTheDatabaseItselfRefusesToRewriteATsOrAnyOtherColumn(t *testing.T) {
	subject := newSubject(t)
	if _, _, err := store(t).Reserve(bg, subject, "slack", "chooser", "C1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store(t).RecordTS(bg, subject, "slack", "chooser", "1.1"); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"change the ts":      `UPDATE surface_messages SET ts = '2.2' WHERE subject_id = $1::uuid`,
		"clear the ts":       `UPDATE surface_messages SET ts = NULL WHERE subject_id = $1::uuid`,
		"change the channel": `UPDATE surface_messages SET channel = 'C2' WHERE subject_id = $1::uuid`,
		"change the time":    `UPDATE surface_messages SET reserved_at = now() - interval '1 day' WHERE subject_id = $1::uuid`,
		"a malformed ts":     `UPDATE surface_messages SET ts = 'abc' WHERE subject_id = $1::uuid`,
	} {
		if _, err := env.DB.Exec(q, subject); err == nil {
			t.Errorf("%s was accepted by the database", name)
		}
	}
}

func TestARefConformsToTheContractBeforeAndAfterItsTs(t *testing.T) {
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	subject := newSubject(t)
	reserved, _, _ := store(t).Reserve(bg, subject, "slack", "bi", "C1")
	posted, _ := store(t).RecordTS(bg, subject, "slack", "bi", "1759587744.000200")
	for name, ref := range map[string]surfacemsg.Ref{"reserved": reserved, "posted": posted} {
		doc, _ := json.Marshal(ref)
		if err := v.Validate("surface_message", doc); err != nil {
			t.Errorf("%s: %v %s", name, err, doc)
		}
	}
}

func TestADatabaseErrorIsReportedNotSwallowed(t *testing.T) {
	ctx, cancel := context.WithCancel(bg)
	cancel()
	subject := newSubject(t)
	if _, _, err := store(t).Reserve(ctx, subject, "slack", "chooser", "C1"); err == nil {
		t.Error("reserve swallowed a cancelled context")
	}
	if _, err := store(t).Get(ctx, subject, "slack", "chooser"); err == nil || errors.Is(err, surfacemsg.ErrNotFound) {
		t.Errorf("get on a cancelled context = %v", err)
	}
	if _, err := store(t).RecordTS(ctx, subject, "slack", "chooser", "1.1"); err == nil || errors.Is(err, surfacemsg.ErrNotFound) {
		t.Errorf("record on a cancelled context = %v", err)
	}
}
