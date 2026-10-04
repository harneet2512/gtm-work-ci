package play

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
)

func TestAdvanceReleasesOneHistoryEpisodeAndRecordsWhatItDid(t *testing.T) {
	r := newEpisodeRig(t)
	ingested := r.ingest.count()

	doc, err := r.svc.NextEpisode(bg, r.manifest)
	if err != nil {
		t.Fatalf("NextEpisode: %v", err)
	}
	validateDef(t, "advanceResult", doc)
	m := resultField(t, doc)
	if m["episode"] != 1.0 || m["total"] != 3.0 || m["manifest_id"] != r.manifest || m["account_id"] != r.world.Account {
		t.Fatalf("advance result: %+v", m)
	}
	// a material episode: the trigger's verdict, no no-action reason
	if m["material"] != true || m["no_action_reason"] != nil {
		t.Fatalf("material episode 1: material=%v reason=%v", m["material"], m["no_action_reason"])
	}
	if m["decision_episode_id"] != nil {
		t.Fatalf("a dry-run history episode links no decision: %+v", m)
	}
	if m["state_version"] != 1.0 || m["state_digest"] == nil {
		t.Fatalf("state link of episode 1: %+v", m)
	}
	if m["graph_diff_id"] == nil {
		t.Fatal("the history event's projection diff is recorded")
	}
	// bookkeeping: one row, cursor at 1, nothing re-ingested
	if got := r.count("demo_episodes"); got != "1" {
		t.Fatalf("demo_episodes = %s", got)
	}
	if got := replaytest.One(t, env.DB, `SELECT released::text FROM demo_replays WHERE manifest_id = $1::uuid`, r.manifest); got != "1" {
		t.Fatalf("cursor = %s", got)
	}
	if r.ingest.count() != ingested {
		t.Fatalf("advance ingested %d events", r.ingest.count()-ingested)
	}
	if got := r.count("source_events"); got != "2" {
		t.Fatalf("source_events = %s (the two history events, nothing new)", got)
	}
}

func TestANonMaterialEpisodeSaysWhyNoActionWasNeeded(t *testing.T) {
	r := newEpisodeRig(t)
	if _, err := r.svc.NextEpisode(bg, r.manifest); err != nil {
		t.Fatal(err)
	}
	doc, err := r.svc.NextEpisode(bg, r.manifest)
	if err != nil {
		t.Fatalf("NextEpisode: %v", err)
	}
	validateDef(t, "advanceResult", doc)
	m := resultField(t, doc)
	if m["episode"] != 2.0 || m["material"] != false || m["no_action_reason"] != "no_material_change" {
		t.Fatalf("non-material episode 2: %+v", m)
	}
	if m["state_version"] != 2.0 {
		t.Fatalf("state_version = %v", m["state_version"])
	}
	if m["account_change_id"] != nil || m["decision_episode_id"] != nil {
		t.Fatalf("a non-material episode links no action: %+v", m)
	}
	var material bool
	var reason sql.NullString
	if err := env.DB.QueryRow(`SELECT material, no_action_reason FROM demo_episodes WHERE manifest_id = $1::uuid AND position = 2`,
		r.manifest).Scan(&material, &reason); err != nil {
		t.Fatal(err)
	}
	if material || !reason.Valid || reason.String != "no_material_change" {
		t.Fatalf("bookkeeping: material=%v reason=%v", material, reason)
	}
}

func TestAdvanceOfTheHeldOutEpisodeRunsPlay(t *testing.T) {
	r := newEpisodeRig(t)
	for i := 1; i <= 2; i++ {
		if _, err := r.svc.NextEpisode(bg, r.manifest); err != nil {
			t.Fatal(err)
		}
	}
	before := r.ingest.count()
	doc, err := r.svc.NextEpisode(bg, r.manifest)
	if err != nil {
		t.Fatalf("NextEpisode(3): %v", err)
	}
	m := resultField(t, doc)
	if m["episode"] != 3.0 || m["material"] != true || m["account_change_id"] == nil {
		t.Fatalf("held-out advance: %+v", m)
	}
	if r.ingest.count() != before+1 {
		t.Fatalf("the held-out advance ingests its event exactly once (%d -> %d)", before, r.ingest.count())
	}
	if got := r.count("source_events"); got != "3" {
		t.Fatalf("source_events = %s", got)
	}
	if got := replaytest.One(t, env.DB, `SELECT status FROM demo_plays WHERE manifest_id = $1::uuid`, r.manifest); got != "complete" {
		t.Fatalf("play status = %s", got)
	}
	if _, err := r.svc.NextEpisode(bg, r.manifest); !errors.Is(err, ErrReplayComplete) {
		t.Fatalf("after the last episode: %v", err)
	}
}

func TestAdvanceAdoptsAPlayAlreadyDone(t *testing.T) {
	r := newEpisodeRig(t)
	for i := 1; i <= 2; i++ {
		if _, err := r.svc.NextEpisode(bg, r.manifest); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.play(); err != nil { // the held-out released directly, outside the episode path
		t.Fatalf("Play: %v", err)
	}
	before := r.ingest.count()
	doc, err := r.svc.NextEpisode(bg, r.manifest)
	if err != nil {
		t.Fatalf("NextEpisode(3): %v", err)
	}
	m := resultField(t, doc)
	if m["episode"] != 3.0 || m["account_change_id"] == nil {
		t.Fatalf("adopted held-out advance: %+v", m)
	}
	if r.ingest.count() != before {
		t.Fatal("adopting a completed Play ingests nothing")
	}
}

func TestReAdvancingAReleasedPositionReturnsItsBookkeeping(t *testing.T) {
	r := newEpisodeRig(t)
	first, err := r.svc.NextEpisode(bg, r.manifest)
	if err != nil {
		t.Fatal(err)
	}
	// roll the cursor back as a reset would; the bookkeeping stays
	if _, err := env.DB.Exec(`UPDATE demo_replays SET released = 0 WHERE manifest_id = $1::uuid`, r.manifest); err != nil {
		t.Fatal(err)
	}
	foot := r.footprint()
	second, err := r.svc.NextEpisode(bg, r.manifest)
	if err != nil {
		t.Fatalf("re-advance: %v", err)
	}
	if string(first) == "" || string(second) == "" {
		t.Fatal("empty result")
	}
	a, b := resultField(t, first), resultField(t, second)
	for _, k := range []string{"episode", "material", "no_action_reason", "state_version", "account_change_id"} {
		if fmt.Sprint(a[k]) != fmt.Sprint(b[k]) {
			t.Fatalf("re-advance %s: %v != %v", k, a[k], b[k])
		}
	}
	if got := r.count("demo_episodes"); got != "1" {
		t.Fatalf("demo_episodes = %s (no second row)", got)
	}
	after := r.footprint()
	for table, n := range foot {
		if table == "demo_replays" {
			continue
		}
		if after[table] != n {
			t.Errorf("%s changed on re-advance: %s -> %s", table, n, after[table])
		}
	}
}

func TestAdvanceFailsOnAHistoryEventTheWorldNeverMaterialized(t *testing.T) {
	r := newEpisodeRig(t)
	// a second manifest over the same account whose history event was never ingested
	ghost := replaytest.Email(9, replaytest.T0.Add(-30*time.Minute), "inbound", "not in the world")
	manifest := replaytest.InsertManifest(t, env.DB, r.world, r.held,
		replaytest.Email(1, replaytest.T0.Add(-2*time.Hour), "inbound", "x"), ghost)
	if _, err := r.svc.NextEpisode(bg, manifest); err != nil {
		t.Fatalf("episode 1: %v", err)
	}
	if _, err := r.svc.NextEpisode(bg, manifest); !errors.Is(err, ErrEpisodeTrail) {
		t.Fatalf("episode 2 (never materialized): %v", err)
	}
}

// newCoalescedRig is a world whose two history events were materialized in one drain: the recompute
// folded both activities into a single shared state diff — the pipeline's real coalescing.
func newCoalescedRig(t *testing.T) *rig {
	t.Helper()
	held := replaytest.NewHeldOut(3)
	history := []normalize.SourceEvent{
		replaytest.Email(1, replaytest.T0.Add(-2*time.Hour), "inbound", "We need the pricing details before signing"),
		replaytest.Email(2, replaytest.T0.Add(-time.Hour), "inbound", "Also the SOC2 report please"),
	}
	r := &rig{t: t, held: held, probe: &fakeProbe{}}
	r.world = replaytest.SeedWorld(t, env.DB)
	r.stack = replaytest.NewStack(t, env.DB)
	for _, ev := range history {
		if _, err := r.stack.Ingest.Ingest(bg, ev); err != nil {
			t.Fatalf("ingest history event: %v", err)
		}
	}
	if err := r.stack.Drain(bg); err != nil {
		t.Fatal(err)
	}
	replaytest.FakeProjector{DB: env.DB}.Complete(t)
	r.finishRuns()
	r.stack.CoalClock.Set(r.stack.CoalClock.Now().Add(31 * time.Minute))
	r.manifest = replaytest.InsertManifest(t, env.DB, r.world, held, history...)
	r.ingest = &countingIngest{inner: r.stack.Ingest}
	r.events = mapSource{refOf(held.Event): held.Event}
	r.svc = r.service(func(o *Options) { o.Rules = episodeRules(t) })
	return r
}

// A history episode inside a coalesced fold reports the fold's shared verdict with coalesced=true, and its
// state_version is the version the world-timed read at the episode bound reports — never the shared diff's
// to_version, whose as_of postdates the bound (the fold also covers the later event).
func TestACoalescedFoldReportsTheSharedVerdictNotAFutureVersion(t *testing.T) {
	r := newCoalescedRig(t)

	doc, err := r.svc.NextEpisode(bg, r.manifest)
	if err != nil {
		t.Fatalf("advance 1: %v", err)
	}
	validateDef(t, "advanceResult", doc)
	m := resultField(t, doc)
	if m["coalesced"] != true {
		t.Fatalf("episode 1 must mark the shared fold: %+v", m)
	}
	var toVersion int
	if err := env.DB.QueryRow(`SELECT to_version FROM state_diffs sd
	  JOIN demo_episodes e ON e.state_diff_id = sd.id
	  WHERE e.manifest_id = $1::uuid AND e.position = 1`, r.manifest).Scan(&toVersion); err != nil {
		t.Fatal(err)
	}
	if v, ok := m["state_version"].(float64); ok && v == float64(toVersion) {
		t.Fatalf("episode 1 names the shared diff's to_version %v — a version past its bound", toVersion)
	}
	var asOf time.Time
	if err := env.DB.QueryRow(`SELECT as_of FROM state_history
	  WHERE account_id = $1::uuid AND version = $2`, r.world.Account, toVersion).Scan(&asOf); err != nil {
		t.Fatal(err)
	}
	var bound time.Time
	if err := env.DB.QueryRow(`SELECT occurred_at FROM activities a
	  JOIN demo_episodes e ON e.activity_id = a.id
	  WHERE e.manifest_id = $1::uuid AND e.position = 1`, r.manifest).Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if !asOf.After(bound) {
		t.Fatalf("test premise broken: the shared version's as_of %v should postdate episode 1's bound %v", asOf, bound)
	}

	// Episode 2 is the other half of the same fold: same verdict, also coalesced.
	doc, err = r.svc.NextEpisode(bg, r.manifest)
	if err != nil {
		t.Fatalf("advance 2: %v", err)
	}
	if m := resultField(t, doc); m["coalesced"] != true || m["state_version"] != float64(toVersion) {
		t.Fatalf("episode 2 (same fold, its bound includes the shared version): %+v", m)
	}
}
