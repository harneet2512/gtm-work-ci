package demorun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The codespace demo freezes two cases, each in its own database, with its own manifest and state file, and
// serves the web app from a production build. These are the seams demorun gives it.

func TestConfigDatabaseSelectsTheCaseDatabase(t *testing.T) {
	cfg := testConfig()
	if got := cfg.DSN(); !strings.Contains(got, "/ghost_demo?") {
		t.Fatalf("default database = %s, want ghost_demo", got)
	}
	cfg.Database = "ghost_case2"
	for name, got := range map[string]string{"DSN": cfg.DSN(), "core": cfg.CoreEnv()["DATABASE_URL"], "tool": cfg.ToolEnv()["DATABASE_URL"]} {
		if !strings.Contains(got, "127.0.0.1:15432/ghost_case2?") {
			t.Errorf("%s = %s, want the case database on the demo port", name, got)
		}
	}
}

func TestWebProdStartsTheProductionServerAndCarriesTheExtraEnv(t *testing.T) {
	cfg := testConfig()
	cfg.WebProd = true
	cfg.WebExtraEnv = Env{"GHOST_DEMO_CONTROL_URL": "http://127.0.0.1:8099"}
	var web *Spec
	for _, s := range cfg.Specs(Tooling{Npm: "npm"}) {
		if s.Name == SvcWeb {
			s := s
			web = &s
		}
	}
	if web == nil {
		t.Fatal("no web spec")
	}
	if got := strings.Join(web.Args, " "); !strings.HasPrefix(got, "run start ") || strings.Contains(got, "dev") {
		t.Fatalf("web args = %q, want the production server (npm run start)", got)
	}
	if web.Env["GHOST_DEMO_CONTROL_URL"] != "http://127.0.0.1:8099" {
		t.Fatal("the control URL must reach the web server (server side only)")
	}
	if _, leaked := web.Env["OPENROUTER_API_KEY"]; leaked {
		t.Fatal("the web app must stay least-privilege")
	}
	dev := testConfig()
	for _, s := range dev.Specs(Tooling{Npm: "npm"}) {
		if s.Name == SvcWeb && !strings.Contains(strings.Join(s.Args, " "), "run dev") {
			t.Fatalf("the local runner keeps the dev server: %v", s.Args)
		}
	}
}

func TestSeedUsesTheFlowsOwnManifestAndStateAndRunsAfterGraphBeforeCoreRestarts(t *testing.T) {
	r := newRig(t)
	caseDir := filepath.Join(r.root, "cases", "case2")
	r.flow.ManifestFile = filepath.Join(caseDir, "manifest.json")
	r.flow.StateFile = filepath.Join(caseDir, "state.json")
	var order []string
	r.toolFn = func(args []string) error { order = append(order, args[0]); return nil }
	r.flow.AfterGraph = func(context.Context) error {
		if s, _ := r.flow.Sup.PIDs.State(SvcCore); s == StateRunning {
			t.Error("core must be down while AfterGraph runs (a template copy needs no sessions)")
		}
		order = append(order, "after-graph")
		return nil
	}
	if err := r.flow.Seed(context.Background(), r.seedOpts()); err != nil {
		t.Fatalf("Seed: %v\n%s", err, r.out.String())
	}
	if got := strings.Join(order, ","); got != "freeze-demo-manifest,graph,after-graph" {
		t.Fatalf("order = %s", got)
	}
	st, ok, err := LoadState(r.flow.StateFile)
	if err != nil || !ok || st.ManifestID != "man-1" {
		t.Fatalf("state not in the flow's own file: %+v %v %v", st, ok, err)
	}
	if _, err := os.Stat(r.flow.Cfg.Layout.StateFile()); err == nil {
		t.Fatal("the default state file must stay untouched when the flow has its own")
	}
	if _, err := os.Stat(r.flow.ManifestFile); err != nil {
		t.Fatalf("the freeze must write the flow's manifest file: %v", err)
	}
}

func TestSeedStopsWhenAfterGraphFails(t *testing.T) {
	r := newRig(t)
	r.flow.AfterGraph = func(context.Context) error { return errors.New("template copy failed") }
	err := r.flow.Seed(context.Background(), r.seedOpts())
	if err == nil || !strings.Contains(err.Error(), "template copy failed") {
		t.Fatalf("err = %v", err)
	}
	if s, _ := r.flow.Sup.PIDs.State(SvcCore); s == StateRunning {
		t.Fatal("core must not start on a world whose snapshot failed")
	}
}
