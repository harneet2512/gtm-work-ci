package graph_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// seedAndIngest empties the database, seeds the vendor company and ingests the events in order.
func seedAndIngest(t *testing.T, events []normalize.SourceEvent) {
	t.Helper()
	resetDB(t)
	company, err := graph.LoadCompany(filepath.Join(fixturesDir, "world", "org.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.SeedCompany(ctx, env.DB, company, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	svc, err := ingest.NewService(env.DB, ingest.Options{Extension: graph.NewExtension(), Clock: clock.NewFixed(t0),
		Debounce: time.Second, MaxWait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ingestAll(t, svc, events...)
}

// checkpointEvents returns the account's events up to and including the checkpoint's last
// file, in ingest order (world files by name, then a live file when the checkpoint names one).
func checkpointEvents(t *testing.T, account, afterFile string) []normalize.SourceEvent {
	t.Helper()
	dir := filepath.Join(fixturesDir, "world", "accounts", account, "events")
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	var paths []string
	for _, p := range names {
		if strings.HasPrefix(afterFile, "live/") || filepath.ToSlash(filepath.Join("world", "accounts", account, "events", filepath.Base(p))) <= afterFile {
			paths = append(paths, p)
		}
	}
	if strings.HasPrefix(afterFile, "live/") {
		paths = append(paths, filepath.Join(fixturesDir, filepath.FromSlash(afterFile)))
	}
	var out []normalize.SourceEvent
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		evs, err := ingest.DecodeEvents(raw)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out = append(out, evs...)
	}
	return out
}

// Edges are scored at every gold checkpoint (cp1-cp4) of every account: the world is re-ingested
// up to the checkpoint's last file and the open edges of the scored types are compared with
// that checkpoint's gold edges. This measures how the graph grows over time (e.g. a contact
// who appears at cp3 must not be in the graph at cp2). It does not measure closed history
// (valid_to) beyond what is open at each checkpoint.
func TestWorldEdgesMatchGoldAtEveryCheckpoint(t *testing.T) {
	fixtureEvents(t) // skips when the fixtures are absent
	scored, total := score{}, 0
	for _, account := range []string{"acme", "beta", "northstar"} {
		for cp := 1; cp <= 4; cp++ {
			raw, err := os.ReadFile(filepath.Join(fixturesDir, "gold", account, "cp"+string(rune('0'+cp))+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var head struct {
				AfterEventFile string `json:"after_event_file"`
			}
			if err := json.Unmarshal(raw, &head); err != nil {
				t.Fatal(err)
			}
			var f goldFile
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			seedAndIngest(t, checkpointEvents(t, account, head.AfterEventFile))

			cluster := map[string]string{}
			for _, e := range f.Expected.Entities {
				for _, id := range e.SourceIdentities {
					if ent := entityOf(t, id); ent != "" {
						cluster[ent] = e.Key
					}
				}
			}
			w := world{cluster: cluster}
			want := map[[3]string]bool{}
			for _, e := range f.Expected.Edges {
				if scoredRels()[e.RelType] {
					want[[3]string{e.Src, e.RelType, e.Dst}] = true
				}
			}
			got, _ := openEdgesByKey(t, w)
			s := scoreSets(want, got)
			t.Logf("%s cp%d (after %s): %d gold edges, %d predicted, %s", account, cp, head.AfterEventFile, s.gold, s.predicted, s)
			for k := range want {
				if !got[k] {
					t.Errorf("%s cp%d: missing edge %v", account, cp, k)
				}
			}
			for k := range got {
				if !want[k] {
					t.Errorf("%s cp%d: unexpected edge %v", account, cp, k)
				}
			}
			scored.tp, scored.gold, scored.predicted = scored.tp+s.tp, scored.gold+s.gold, scored.predicted+s.predicted
			total++
		}
	}
	t.Logf("all %d checkpoints together: %s", total, scored)
	if scored.precision() < minScore || scored.recall() < minScore {
		t.Errorf("checkpoint edge precision %.4f / recall %.4f below %.2f", scored.precision(), scored.recall(), minScore)
	}
}
