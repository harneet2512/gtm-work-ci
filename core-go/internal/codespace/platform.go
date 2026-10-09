package codespace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// Service starts and stops one supervised process (demorun.Supervisor).
type Service interface {
	Start(ctx context.Context, spec demorun.Spec) error
	Stop(ctx context.Context, spec demorun.Spec) error
}

// InvisibilityChecker is core's event-N-invisible assertion (demorun.CoreClient).
type InvisibilityChecker interface {
	Invisibility(ctx context.Context, manifestID string) (demorun.Invisibility, error)
}

// RealPlatform is the Platform of a running codespace: the runner's supervisor, the in-process ghostctl tools and
// core's HTTP API. Core's spec is the runner's, with only the database swapped for the case's.
type RealPlatform struct {
	Base         demorun.Config
	Services     Service
	Core         InvisibilityChecker
	RunTool      func(args []string) error
	ApplyToolEnv func(demorun.Env) error

	// toolMu serializes the tools: they read DATABASE_URL from the process environment.
	toolMu sync.Mutex
}

// CoreBinary is where `demo up` (Prepare) builds core.
func CoreBinary(l demorun.Layout) string {
	name := "core"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(l.BinDir(), name)
}

func (p *RealPlatform) cfgFor(c Case) demorun.Config {
	cfg := p.Base.ForGraph(c.Graph) // core and the tools read this case's own graph
	cfg.Database = c.Database
	return cfg
}

// CoreSpec is the supervised core process for a case: the runner's plan with the case's database.
func (p *RealPlatform) CoreSpec(c Case) (demorun.Spec, error) {
	for _, s := range p.cfgFor(c).Specs(demorun.Tooling{Core: CoreBinary(p.Base.Layout)}) {
		if s.Name == demorun.SvcCore {
			return s, nil
		}
	}
	return demorun.Spec{}, errors.New("codespace: the service plan has no core")
}

// StopCore implements Platform.
func (p *RealPlatform) StopCore(ctx context.Context) error {
	spec, err := p.CoreSpec(Case{})
	if err != nil {
		return err
	}
	return p.Services.Stop(ctx, spec)
}

// StartCore implements Platform.
func (p *RealPlatform) StartCore(ctx context.Context, c Case) error {
	spec, err := p.CoreSpec(c)
	if err != nil {
		return err
	}
	return p.Services.Start(ctx, spec)
}

// storeSpecs are Postgres and every Neo4j, in start order. The host binary is the copy `demo up` installs in the bin directory.
func (p *RealPlatform) storeSpecs() []demorun.Spec {
	host := filepath.Join(p.Base.Layout.BinDir(), "ghostctl-host")
	if runtime.GOOS == "windows" {
		host += ".exe"
	}
	var specs []demorun.Spec
	for _, s := range p.Base.Specs(demorun.Tooling{Host: host}) {
		if s.Name == demorun.SvcPostgres || strings.HasPrefix(s.Name, demorun.SvcNeo4j) {
			specs = append(specs, s)
		}
	}
	return specs
}

// eachStore runs fn on every store spec at the same time and joins the errors: the stores are independent of each other, so
// their (slow) JVM and database start-ups and graceful stops overlap instead of adding up.
func eachStore(ctx context.Context, specs []demorun.Spec, fn func(context.Context, demorun.Spec) error) error {
	errs := make([]error, len(specs))
	var wg sync.WaitGroup
	for i, s := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = fn(ctx, s)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// StopStores implements Platform: Postgres and every Neo4j are stopped together, so every file of the live copy is closed.
// Every store is asked to stop even when one fails; the errors are joined.
func (p *RealPlatform) StopStores(ctx context.Context) error {
	return eachStore(ctx, p.storeSpecs(), p.Services.Stop)
}

// StartStores implements Platform: Postgres and every Neo4j start together; it returns when all are healthy.
func (p *RealPlatform) StartStores(ctx context.Context) error {
	return eachStore(ctx, p.storeSpecs(), p.Services.Start)
}

// RebuildGraph implements Platform (the one-time setup only): `ghostctl graph rebuild` against the case's database (it wipes Neo4j first).
func (p *RealPlatform) RebuildGraph(_ context.Context, c Case) error {
	p.toolMu.Lock()
	defer p.toolMu.Unlock()
	if err := p.ApplyToolEnv(p.cfgFor(c).ToolEnv()); err != nil {
		return err
	}
	if err := p.RunTool([]string{"graph", "rebuild"}); err != nil {
		return fmt.Errorf("codespace: rebuild the graph from %s: %w", c.Label, err)
	}
	return nil
}

// Invisibility implements Platform.
func (p *RealPlatform) Invisibility(ctx context.Context, manifestID string) (demorun.Invisibility, error) {
	return p.Core.Invisibility(ctx, manifestID)
}

var _ Platform = (*RealPlatform)(nil)
