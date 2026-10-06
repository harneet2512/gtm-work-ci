package codespace

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

type fakeService struct {
	mu      sync.Mutex
	started []demorun.Spec
	stopped []demorun.Spec
	err     error
}

func (f *fakeService) Start(_ context.Context, s demorun.Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, s)
	return f.err
}
func (f *fakeService) Stop(_ context.Context, s demorun.Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, s)
	return f.err
}

type fakeCore struct {
	inv demorun.Invisibility
	err error
	ids []string
}

func (f *fakeCore) Invisibility(_ context.Context, id string) (demorun.Invisibility, error) {
	f.ids = append(f.ids, id)
	return f.inv, f.err
}

func testBase() demorun.Config {
	return demorun.Config{
		Layout:  demorun.NewLayout(filepath.Join("repo")),
		Ports:   demorun.Ports{Core: 8080, Worker: 8090, Web: 3000, Postgres: 15432, Bolt: 17687},
		DotEnv:  demorun.Env{"DATABASE_URL": "postgres://neon.example/prod"},
		Process: demorun.Env{},
		Secrets: demorun.Env{"GHOST_API_TOKEN": "t0k", "GHOST_RUN_TOKEN_SECRET": "s3c", "NEO4J_PASSWORD": "pw"},
		LLMMode: "live",
	}
}

func TestRealPlatformStartsCoreOnTheCasesDatabaseOnly(t *testing.T) {
	svc := &fakeService{}
	p := &RealPlatform{Base: testBase(), Services: svc}
	c2 := DefaultCases()[1]
	if err := p.StartCore(context.Background(), c2); err != nil {
		t.Fatal(err)
	}
	spec := svc.started[0]
	if spec.Name != demorun.SvcCore {
		t.Fatalf("started %s, want core", spec.Name)
	}
	if got := spec.Env["DATABASE_URL"]; !strings.Contains(got, "127.0.0.1:15432/ghost_case2?") || strings.Contains(got, "neon") {
		t.Fatalf("core DATABASE_URL = %s, want the case database of the demo cluster and never the .env one", got)
	}
	if !strings.HasSuffix(filepath.ToSlash(spec.Path), "/.demo/bin/core") && !strings.HasSuffix(filepath.ToSlash(spec.Path), "/.demo/bin/core.exe") {
		t.Fatalf("core binary = %s", spec.Path)
	}
	// The base config is not modified (a value copy): the next case gets its own database.
	if err := p.StartCore(context.Background(), DefaultCases()[0]); err != nil {
		t.Fatal(err)
	}
	if got := svc.started[1].Env["DATABASE_URL"]; !strings.Contains(got, "/ghost_case1?") {
		t.Fatalf("second start DATABASE_URL = %s", got)
	}
}

func TestRealPlatformStopCoreStopsTheCoreService(t *testing.T) {
	svc := &fakeService{}
	p := &RealPlatform{Base: testBase(), Services: svc}
	if err := p.StopCore(context.Background()); err != nil || len(svc.stopped) != 1 || svc.stopped[0].Name != demorun.SvcCore {
		t.Fatalf("stop = %v, %v", svc.stopped, err)
	}
	svc.err = errors.New("will not stop")
	if err := p.StopCore(context.Background()); err == nil {
		t.Fatal("a stop error must surface")
	}
	if err := (&RealPlatform{Base: testBase(), Services: svc}).StartCore(context.Background(), DefaultCases()[0]); err == nil {
		t.Fatal("a start error must surface")
	}
}

func TestRealPlatformRebuildsTheGraphWithTheCasesToolEnvironment(t *testing.T) {
	var applied demorun.Env
	var ran [][]string
	p := &RealPlatform{Base: testBase(),
		ApplyToolEnv: func(e demorun.Env) error { applied = e; return nil },
		RunTool:      func(args []string) error { ran = append(ran, args); return nil }}
	p.Base.GraphInstances = 2
	if err := p.RebuildGraph(context.Background(), DefaultCases()[1]); err != nil {
		t.Fatal(err)
	}
	// case 2 is projected into its own Neo4j (Bolt+1), never into case 1's
	if !strings.Contains(applied["DATABASE_URL"], "/ghost_case2?") || applied["NEO4J_URI"] != "bolt://127.0.0.1:17688" {
		t.Fatalf("tool env = %v %s", applied.Names(), applied["NEO4J_URI"])
	}
	spec, err := p.CoreSpec(DefaultCases()[1])
	if err != nil || spec.Env["NEO4J_URI"] != "bolt://127.0.0.1:17688" {
		t.Fatalf("core on case 2 reads its own graph: %v %v", spec.Env["NEO4J_URI"], err)
	}
	if first, _ := p.CoreSpec(DefaultCases()[0]); first.Env["NEO4J_URI"] != "bolt://127.0.0.1:17687" {
		t.Fatalf("core on case 1 reads the original graph: %v", first.Env["NEO4J_URI"])
	}
	if len(ran) != 1 || strings.Join(ran[0], " ") != "graph rebuild" {
		t.Fatalf("ran = %v, want a wiping rebuild (no --keep)", ran)
	}
	p.RunTool = func([]string) error { return errors.New("neo4j refused") }
	if err := p.RebuildGraph(context.Background(), DefaultCases()[0]); err == nil || !strings.Contains(err.Error(), "MedTech Advances") {
		t.Fatalf("err = %v, want the case named", err)
	}
	p.ApplyToolEnv = func(demorun.Env) error { return errors.New("env") }
	if err := p.RebuildGraph(context.Background(), DefaultCases()[0]); err == nil {
		t.Fatal("an environment error must surface")
	}
}

func TestRealPlatformStopsAndStartsOnlyTheStores(t *testing.T) {
	svc := &fakeService{}
	cfg := testBase()
	cfg.GraphInstances = 2
	p := &RealPlatform{Base: cfg, Services: svc}
	if err := p.StartStores(context.Background()); err != nil {
		t.Fatal(err)
	}
	names := func(specs []demorun.Spec) string {
		var out []string
		for _, s := range specs {
			out = append(out, s.Name)
		}
		sort.Strings(out) // the stores start together, so their order is not defined
		return strings.Join(out, ",")
	}
	if got := names(svc.started); got != "neo4j,neo4j-case2,postgres" {
		t.Fatalf("started = %s", got)
	}
	if !strings.Contains(filepath.ToSlash(svc.started[0].Path), "/bin/ghostctl-host") {
		t.Fatalf("the stores run from the installed host copy: %s", svc.started[0].Path)
	}
	svc.err = errors.New("stuck")
	if err := p.StopStores(context.Background()); err == nil || !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("err = %v", err)
	}
	if got := names(svc.stopped); got != "neo4j,neo4j-case2,postgres" {
		t.Fatalf("every store is stopped even after a failure: %s", got)
	}
	if err := p.StartStores(context.Background()); err == nil || !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("a failed start must be reported: %v", err)
	}
}

func TestRealPlatformInvisibilityAsksCore(t *testing.T) {
	core := &fakeCore{inv: demorun.Invisibility{Status: "withheld"}}
	p := &RealPlatform{Core: core}
	inv, err := p.Invisibility(context.Background(), "man-9")
	if err != nil || inv.Status != "withheld" || core.ids[0] != "man-9" {
		t.Fatalf("%+v %v %v", inv, err, core.ids)
	}
}

func TestCoreBinaryLivesInTheDemoBinDirectory(t *testing.T) {
	l := demorun.NewLayout("repo")
	if got := filepath.ToSlash(CoreBinary(l)); !strings.HasPrefix(got, "repo/.demo/bin/core") {
		t.Fatalf("CoreBinary = %s", got)
	}
}
