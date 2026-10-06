package play

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/payloadhash"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
)

// dealIngest is the pipeline attributing the released activity to a deal (email carries no deal hint of its own).
type dealIngest struct {
	inner Ingester
	deal  string
}

func (d dealIngest) Ingest(ctx context.Context, ev normalize.SourceEvent) (ingest.Result, error) {
	res, err := d.inner.Ingest(ctx, ev)
	if err != nil {
		return res, err
	}
	_, err = env.DB.ExecContext(ctx, `UPDATE activities SET opportunity_id = $2::uuid WHERE id = $1::uuid`, res.ActivityID, d.deal)
	return res, err
}

func (r *rig) otherDeal() string {
	return replaytest.One(r.t, env.DB, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, 'Acme renewal', 'renewal') RETURNING id::text`, r.world.Account)
}

func TestPlayRefusesAReleaseAttributedToAnotherDeal(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	svc := r.service(func(o *Options) { o.Ingest = dealIngest{inner: r.ingest, deal: r.otherDeal()} })
	for i := 0; i < 2; i++ { // every retry gets the same answer
		if _, err := svc.Play(bg, Request{ManifestID: r.manifest}); !errors.Is(err, ErrReleaseMismatch) {
			t.Fatalf("Play #%d: %v, want ErrReleaseMismatch", i+1, err)
		}
	}
	if r.count("account_changes") != "0" || replaytest.One(t, env.DB, `SELECT status FROM demo_plays`) != "released" {
		t.Fatal("a release on another deal must not complete")
	}
}

func TestPlayTakesTheDealFromThePipeline(t *testing.T) {
	cases := map[string]struct {
		deal func(*rig) string // the deal the pipeline attributes the activity to; "" leaves it unattributed
		want func(*rig) any    // the opportunity_id of the change and of the update
	}{
		"the manifest's deal": {func(r *rig) string { return r.world.Opportunity }, func(r *rig) any { return r.world.Opportunity }},
		"no deal":             {func(*rig) string { return "" }, func(*rig) any { return nil }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, replaytest.NewHeldOut(2))
			svc := r.svc
			if deal := tc.deal(r); deal != "" {
				svc = r.service(func(o *Options) { o.Ingest = dealIngest{inner: r.ingest, deal: deal} })
			}
			if _, err := svc.Play(bg, Request{ManifestID: r.manifest}); err != nil {
				t.Fatalf("Play: %v", err)
			}
			want := tc.want(r)
			tables := []string{"account_changes"}
			if r.count("business_intelligence_updates") == "1" { // a deal-scoped diff may not be material
				tables = append(tables, "business_intelligence_updates")
			}
			for _, table := range tables {
				var got any
				var s *string
				if err := env.DB.QueryRow(`SELECT opportunity_id::text FROM ` + table).Scan(&s); err != nil {
					t.Fatal(err)
				}
				if s != nil {
					got = *s
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s.opportunity_id = %v, want %v", table, got, want)
				}
			}
		})
	}
}

// The manifest pins event N's payload: a dataset that was edited after the case was frozen is refused before
// anything is released.
func TestPlayRefusesADatasetPayloadThatIsNotTheOneTheManifestPinned(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	edited := r.held.Event
	edited.Payload = []byte(strings.Replace(string(edited.Payload), "SOC2 report", "SOC2 report and the pen-test summary", 1))
	if string(edited.Payload) == string(r.held.Event.Payload) {
		t.Fatal("setup: the payload must differ")
	}
	r.events[refOf(edited)] = edited // same idempotency key, same time, same origin: only the payload differs
	before := r.footprint()
	_, err := r.play()
	if !errors.Is(err, ErrReleaseMismatch) || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("Play: %v, want ErrReleaseMismatch naming the payload", err)
	}
	if after := r.footprint(); !reflect.DeepEqual(before, after) {
		t.Fatalf("a refused Play wrote something:\nbefore %v\nafter  %v", before, after)
	}
	if r.ingest.count() != 0 {
		t.Fatal("a refused Play released the event")
	}
}

// Key order and whitespace are not content: the same payload re-serialized is accepted.
func TestPlayAcceptsTheSamePayloadSerializedDifferently(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	var doc map[string]any
	if err := json.Unmarshal(r.held.Event.Payload, &doc); err != nil {
		t.Fatal(err)
	}
	pretty, _ := json.MarshalIndent(doc, "", "    ")
	re := r.held.Event
	re.Payload = pretty
	r.events[refOf(re)] = re
	if _, err := r.play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
}

func TestAPayloadPinIsRequiredAndCompared(t *testing.T) {
	payload := []byte(`{"a":1,"b":[true]}`)
	pin, err := payloadhash.SHA256(payload)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		pin     string
		payload []byte
		wantErr bool
	}{
		"matches":                {pin, payload, false},
		"re-serialized":          {pin, []byte(`{ "b": [true], "a": 1 }`), false},
		"a manifest with no pin": {"", payload, true},
		"another payload":        {pin, []byte(`{"a":2,"b":[true]}`), true},
		"not JSON":               {pin, []byte(`nope`), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := checkPayloadPin(tc.pin, tc.payload)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkPayloadPin = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrReleaseMismatch) {
				t.Fatalf("a refusal is a release mismatch: %v", err)
			}
		})
	}
}
