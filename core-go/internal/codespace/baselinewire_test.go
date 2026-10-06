package codespace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

func wiredConfig(t *testing.T) demorun.Config {
	t.Helper()
	home := t.TempDir()
	l := demorun.NewLayout(filepath.Join(home, "repo"))
	l.StateDir = filepath.Join(home, "ghost-demo")
	l.Live = filepath.Join(l.StateDir, "live")
	cfg := testBase()
	cfg.Layout = l
	cfg.GraphInstances = 2
	return cfg
}

func TestNewBaselineSealsTheStatefulPathsOfTheLiveCopyAndNothingElse(t *testing.T) {
	cfg := wiredConfig(t)
	b := NewBaseline(cfg, DefaultCases())
	want := []string{"live/pg/data", "live/neo4j/data", "live/neo4j/tx", "live/neo4j-case2/data", "live/neo4j-case2/tx",
		"live/cases", "live/replay-events", "live/active-case", "live/graph-case1", "live/graph-case2"}
	if strings.Join(b.Paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v\nwant    %v", b.Paths, want)
	}
	if b.Home != cfg.Layout.Dir() || b.Dir() != filepath.Join(cfg.Layout.Dir(), "baseline") {
		t.Fatalf("the baseline sits under the demo home: %s", b.Dir())
	}
	if strings.Join(b.Cases, ",") != "case1,case2" {
		t.Fatalf("cases = %v", b.Cases)
	}
	for _, p := range b.Paths {
		for _, outside := range []string{"cassettes", "logs", "pids", "bin", "secrets"} {
			if strings.Contains(p, outside) {
				t.Errorf("%s must stay outside the baseline and the live copy", p)
			}
		}
	}
}

func TestNewBaselineWithoutALiveDirectoryStillMirrorsTheStateDir(t *testing.T) {
	cfg := wiredConfig(t)
	cfg.Layout.Live = ""
	b := NewBaseline(cfg, DefaultCases())
	if b.Paths[0] != "pg/data" || b.Paths[len(b.Paths)-1] != "graph-case2" {
		t.Fatalf("paths = %v", b.Paths)
	}
}

func TestSealRecordingAddsTheRunSheetAndNotesTheCacheWithoutRestoringEither(t *testing.T) {
	cfg := wiredConfig(t)
	home := cfg.Layout.Dir()
	writeTree(t, home, map[string]string{
		"live/pg/data/PG_VERSION": "17", "live/neo4j/data/g": "1", "live/neo4j/tx/t": "1", "live/neo4j-case2/data/g": "2",
		"live/neo4j-case2/tx/t": "2", "live/replay-events/e.json": "[]", "live/active-case": "case1\n",
		"live/graph-case1": "case1\n", "live/graph-case2": "case2\n", "cassettes/a.json": "x",
		"live/cases/case1/state.json": `{"held_out_event_id":"h1"}`, "live/cases/case1/manifest.json": manifestJSON(2, "h1"),
		"live/cases/case2/state.json": `{"held_out_event_id":"h2"}`, "live/cases/case2/manifest.json": manifestJSON(3, "h2"),
	})
	rt := Runtime{Cfg: cfg, Baseline: NewBaseline(cfg, DefaultCases())}
	if _, err := rt.Baseline.Seal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.SealRecording(context.Background()); err == nil {
		t.Fatal("no run sheet yet: the record run did not finish, so the seal must say so")
	}
	writeTree(t, home, map[string]string{RunSheetName: "press Play"})
	if err := rt.SealRecording(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := rt.Baseline.Manifest()
	if err != nil || m.Component(RunSheetName) == nil || m.Cache == nil || m.Cache.Path != "cassettes" || m.Cache.Files != 1 {
		t.Fatalf("manifest = %+v err=%v", m, err)
	}
	if m.Component("cassettes") != nil {
		t.Fatal("the cache is noted, never sealed or restored")
	}
	if err := rt.Baseline.Verify(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rt.Baseline.Dir(), "cassettes")); err == nil {
		t.Fatal("the baseline must not hold a copy of the model-call cache")
	}
}

func manifestJSON(history int, heldOut string) string {
	var ev []string
	for i := 1; i <= history; i++ {
		ev = append(ev, `{"event":{"replay_position":`+string(rune('0'+i))+`}}`)
	}
	return `{"events":[` + strings.Join(ev, ",") + `],"held_out_event":{"event_id":"` + heldOut + `","replay_position":` + string(rune('0'+history+1)) + `}}`
}
