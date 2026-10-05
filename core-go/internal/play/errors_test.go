package play

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
)

type failingIngest struct{}

func (failingIngest) Ingest(context.Context, normalize.SourceEvent) (ingest.Result, error) {
	return ingest.Result{}, errors.New("ingest is down")
}

type failingDrain struct{}

func (failingDrain) Drain(context.Context) (coalesce.DrainResult, error) {
	return coalesce.DrainResult{Failed: 1}, errors.New("the model worker is down")
}

type failingBarrier struct{}

func (failingBarrier) Wait(context.Context, string, time.Duration) error {
	return errors.New("neo4j fell over")
}

func TestPlayFailuresKeepTheReleaseResumable(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Options)
		want   string
		events string // source_events after the failure
	}{
		"ingest fails":     {func(o *Options) { o.Ingest = failingIngest{} }, "ingest is down", "2"},
		"recompute fails":  {func(o *Options) { o.Recompute = failingDrain{} }, "the model worker is down", "3"},
		"projection fails": {func(o *Options) { o.Graph = failingBarrier{} }, "neo4j fell over", "3"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, replaytest.NewHeldOut(2))
			svc := r.service(tc.mutate)
			_, err := svc.Play(bg, Request{ManifestID: r.manifest})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Play: %v, want an error naming %q", err, tc.want)
			}
			if got := r.count("source_events"); got != tc.events {
				t.Errorf("source_events = %s, want %s", got, tc.events)
			}
			if r.count("account_changes") != "0" {
				t.Error("a failed Play wrote a change")
			}
			if _, err := r.play(); err != nil { // the next Play, with everything working, finishes the job
				t.Fatalf("resume: %v", err)
			}
			if got := r.count("account_changes") + "/" + r.count("business_intelligence_updates"); got != "1/1" {
				t.Fatalf("changes/updates after the resume = %s", got)
			}
		})
	}
}

func TestOptionsAreValidated(t *testing.T) {
	if _, err := NewService(Options{}); err == nil {
		t.Error("a service needs a database and an ingest path")
	}
	if _, err := NewChecker(nil, nil); err == nil {
		t.Error("a checker needs a database")
	}
	r := newRig(t, replaytest.NewHeldOut(2))
	svc, err := NewService(Options{DB: env.DB, Ingest: r.ingest})
	if err != nil || svc.poll != DefaultPoll || svc.timeout != DefaultTimeout || svc.clk == nil {
		t.Fatalf("defaults: %+v, %v", svc, err)
	}
}

func TestTheVisibleErrorSaysHowMuchLeaked(t *testing.T) {
	e := &VisibleError{Report: Report{HeldOutEventID: "e1", Leaks: []Leak{{}, {}}}}
	if got := e.Error(); !strings.Contains(got, "e1") || !strings.Contains(got, "2 thing") {
		t.Fatalf("message = %q", got)
	}
}

func TestTheReplayWorldListsTheDealsOfTheAccount(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	if _, err := env.DB.Exec(`INSERT INTO opportunity_state_history (opportunity_id, account_id, version, as_of, state)
		VALUES ($1::uuid, $2::uuid, 1, now(), '{}')`, r.world.Opportunity, r.world.Account); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO opportunity_state (opportunity_id, account_id, version, as_of, is_open, state)
		VALUES ($1::uuid, $2::uuid, 1, now(), true, '{}')`, r.world.Opportunity, r.world.Account); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(bg, env.DB, r.manifest)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := r.svc.stateRefs(bg, m.AccountID)
	if err != nil || len(refs) != 2 || refs[0].OpportunityID != nil || refs[1].OpportunityID == nil || *refs[1].OpportunityID != r.world.Opportunity {
		t.Fatalf("state refs = %+v, %v", refs, err)
	}
}
