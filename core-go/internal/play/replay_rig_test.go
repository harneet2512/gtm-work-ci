package play

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

// validateDef checks a document against one $defs entry of a contract schema.
func validateDef(t *testing.T, def string, doc []byte) {
	t.Helper()
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.ValidateDef("episode_replay", def, doc); err != nil {
		t.Fatalf("%s violates its schema: %v\n%s", def, err, doc)
	}
}

// repoFile walks up from the test's working directory to a repository-relative file.
func repoFile(t testing.TB, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return filepath.Join(dir, rel)
		}
		if filepath.Dir(dir) == dir {
			t.Fatalf("%s not found above the working directory", rel)
		}
		dir = filepath.Dir(dir)
	}
}

// episodeRules are the lifecycle rules the episode view's knowledge-as-of read replays.
func episodeRules(t testing.TB) *knowledge.Rules {
	t.Helper()
	r, err := knowledge.LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &r
}

// newEpisodeRig is a world whose two history events were materialized one at a time (a drain and a
// projection each), so every episode has its own state version, diff and trigger evaluation. Event 1 asks
// for something (a blocker — material; the first diff is always material anyway); event 2 is noise
// ("Thanks for the update" — non-material). The held-out SOC2 email is event 3.
func newEpisodeRig(t *testing.T) *rig {
	t.Helper()
	held := replaytest.NewHeldOut(3)
	history := []normalize.SourceEvent{
		replaytest.Email(1, replaytest.T0.Add(-2*time.Hour), "inbound", "We need the pricing details before signing"),
		replaytest.Email(2, replaytest.T0.Add(-time.Hour), "inbound", "Thanks for the update"),
	}
	r := &rig{t: t, held: held, probe: &fakeProbe{}}
	r.world = replaytest.SeedWorld(t, env.DB)
	r.stack = replaytest.NewStack(t, env.DB)
	for _, ev := range history {
		if _, err := r.stack.Ingest.Ingest(bg, ev); err != nil {
			t.Fatalf("ingest history event: %v", err)
		}
		if err := r.stack.Drain(bg); err != nil {
			t.Fatal(err)
		}
		replaytest.FakeProjector{DB: env.DB}.Complete(t)
		r.finishRuns()
	}
	// The world was loaded before the replay starts: episode 1's eligible evaluation is older than the
	// account's eligibility cooldown (30m) when the held-out event lands.
	r.stack.CoalClock.Set(r.stack.CoalClock.Now().Add(31 * time.Minute))
	r.manifest = replaytest.InsertManifest(t, env.DB, r.world, held, history...)
	r.ingest = &countingIngest{inner: r.stack.Ingest}
	r.events = mapSource{refOf(held.Event): held.Event}
	r.svc = r.service(func(o *Options) { o.Rules = episodeRules(t) })
	return r
}

// finishRuns records the runs a history drain opened: in the replayed world the human finished each
// historical run (a dry run's terminal status is "recorded"), so an eligible episode frees the account for
// the next event instead of leaving it blocked on open_run_exists forever.
func (r *rig) finishRuns() {
	if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'recorded', updated_at = now()
	  WHERE status IN ('pending','context_built','drafted','awaiting_human','approved','edited')`); err != nil {
		r.t.Fatalf("finish runs: %v", err)
	}
}

// resultField unmarshals the advance/reset/view result into a map.
func resultField(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	return m
}

// advanceEpisodes releases the first n episodes through NextEpisode.
func advanceEpisodes(t *testing.T, r *rig, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := r.svc.NextEpisode(bg, r.manifest); err != nil {
			t.Fatalf("advance %d: %v", i+1, err)
		}
	}
}

// seedKnowledge inserts a knowledge object and the evidence that earns it provisional status (one decision
// episode and one positive customer reaction, the lifecycle's first rung), with every timestamp set
// explicitly so an as-of read sees it exactly when the bound is past the evidence times.
func seedKnowledge(t *testing.T, title string, createdAt, evidenceAt time.Time) string {
	t.Helper()
	id := replaytest.One(t, env.DB, `INSERT INTO knowledge (title, guidance, created_at) VALUES ($1, 'g', $2) RETURNING id::text`,
		title, createdAt.UTC())
	if _, err := env.DB.Exec(`INSERT INTO knowledge_evidence (knowledge_id, kind, ref_id, note, created_at) VALUES
	  ($1::uuid, 'decision_episode', '55555555-5555-4555-8555-555555555555'::uuid, '', $2),
	  ($1::uuid, 'customer_reaction', '66666666-6666-4666-8666-666666666666'::uuid, 'polarity=positive', $3)`,
		id, evidenceAt.UTC(), evidenceAt.UTC().Add(time.Minute)); err != nil {
		t.Fatalf("seed knowledge evidence: %v", err)
	}
	return id
}
