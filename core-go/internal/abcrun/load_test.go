package abcrun_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/abcrun"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

func writeTemp(t *testing.T, name string, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPackAndLoadLearningRoundTripAndRefuseTheWrongVersion(t *testing.T) {
	p := pack("discriminating")
	got, err := abcrun.LoadPack(writeTemp(t, "pack.json", p))
	if err != nil || len(got.Situations) != 1 || got.Situations[0].ID != "S-1" {
		t.Fatalf("pack %v %v", got.Situations, err)
	}
	if _, err := abcrun.LoadPack(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("a missing pack must be an error")
	}
	if _, err := abcrun.LoadPack(writeTemp(t, "bad.json", map[string]any{"version": "x"})); err == nil {
		t.Fatal("a pack of another version must be refused")
	}
	l := abcrun.Learning{Version: abcrun.LearningVersion, Cutoff: time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC), Knowledge: items()}
	back, err := abcrun.LoadLearning(writeTemp(t, "learning.json", l))
	if err != nil || len(back.Knowledge) != 2 || back.Knowledge[0].Key != "K202" {
		t.Fatalf("learning %+v %v", back, err)
	}
	if _, err := abcrun.LoadLearning(writeTemp(t, "old.json", map[string]any{"version": "uplift_learning.v0"})); err == nil {
		t.Fatal("learning evidence of another version must be refused")
	}
	if _, err := abcrun.LoadLearning(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing learning evidence must be an error")
	}
}

func TestInstallStoreRefusesANonEmptyStoreAndReplaceStoreSwapsTheContents(t *testing.T) {
	newBuilder(t)
	rules, err := knowledge.LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := abcrun.InstallStore(bg, env.DB, rules, items()); err != nil {
		t.Fatal(err)
	}
	if err := abcrun.InstallStore(bg, env.DB, rules, items()); err == nil || !strings.Contains(err.Error(), "must start empty") {
		t.Fatalf("a second learning pass over a non-empty store must be refused: %v", err)
	}
	if err := abcrun.ReplaceStore(bg, env.DB, rules, items()[:1]); err != nil {
		t.Fatal(err)
	}
	store, err := abcrun.ExportStore(bg, env.DB)
	if err != nil || len(store) != 1 || *store[0].Key != "K202" {
		t.Fatalf("store after replace: %+v %v", store, err)
	}
	if store[0].Provenance.CreatedFrom != "human_delta" || len(store[0].SupportingDecisionEpisodeIDs) != 1 {
		t.Fatalf("the item must carry its source episode and earn its support from evidence: %+v", store[0])
	}
}

func TestPlanBuildsEveryWorldAndReportsWhatEachArmWouldReadWithoutAModel(t *testing.T) {
	rg := newRig(t, pack("discriminating", "exception"), nil)
	rows, store, err := rg.runner.Plan(bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(store) != 2 || rg.scripts.nstrats != 0 {
		t.Fatalf("rows %d store %d model calls %d", len(rows), len(store), rg.scripts.nstrats)
	}
	if rows[0].Generations != 3 || rows[1].Generations != 2 || len(rows[0].BSet) != 1 || len(rows[0].CSet) != 1 {
		t.Fatalf("plan = %+v", rows)
	}
	if rows[1].Error != "" || !strings.Contains(rows[0].StateHeader, "stage Quote") {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestPlanReportsASelectionErrorPerSituationInsteadOfStopping(t *testing.T) {
	rg := newRig(t, pack("exception"), nil) // an exception situation in a world where the item applies
	rows, _, err := rg.runner.Plan(bg)
	if err != nil || len(rows) != 1 || !strings.Contains(rows[0].Error, "selection error") {
		t.Fatalf("rows %+v err %v", rows, err)
	}
}

func TestStartWorkerStartsTheRealWorkerInReplayModeAndStopsIt(t *testing.T) {
	py := os.Getenv("GHOST_WORKER_PYTHON")
	if py == "" {
		py = "python"
	}
	if err := exec.Command(py, "-c", "import uvicorn, fastapi, ghost_worker").Run(); err != nil {
		t.Skip("the worker's Python dependencies are not installed here")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var logged []string
	w, err := abcrun.StartWorker(ctx, abcrun.WorkerSpec{Python: py, RepoRoot: filepath.Dir(filepath.Dir(repoFile(t, "contracts"))), Mode: "replay",
		CassetteDir: t.TempDir(), CoreURL: "http://127.0.0.1:1", StartTimeout: time.Minute, Logf: func(f string, a ...any) { logged = append(logged, f) }})
	if err != nil {
		t.Skipf("the worker could not start on this machine: %v", err)
	}
	if !strings.HasPrefix(w.URL, "http://127.0.0.1:") {
		t.Fatalf("url %s", w.URL)
	}
	w.Stop()
	w.Stop() // stopping twice is harmless
}
