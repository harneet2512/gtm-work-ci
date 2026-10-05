package demorun

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph/neo4jtest"
	"github.com/harneet2512/gtm-work/core-go/internal/store/embedded"
)

// Up starts the services in order and stops at the first failure with a *ServiceError naming it. Services that
// were already up stay up, so fixing the cause and running `demo up` again resumes from the failed one.
func Up(ctx context.Context, sup Supervisor, specs []Spec) error {
	for _, spec := range specs {
		if st, _ := sup.PIDs.State(spec.Name); st != StateRunning {
			if err := CheckPortFree(spec); err != nil {
				sup.say("FAILED service %s: %v", spec.Name, err)
				return err
			}
		}
		if err := sup.Start(ctx, spec); err != nil {
			sup.say("FAILED service %s: %v", spec.Name, err)
			return err
		}
	}
	return nil
}

// Down stops the services in reverse start order. It tries every service and joins the errors.
func Down(ctx context.Context, sup Supervisor, specs []Spec) error {
	var errs []error
	for i := len(specs) - 1; i >= 0; i-- {
		if err := sup.Stop(ctx, specs[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Reset stops everything and deletes the demo's data: the Postgres cluster, the Neo4j directory, the replay
// dataset, the manifest, the state file and the logs. Binaries, the Python environment and the generated
// secrets are kept (the Neo4j password is regenerated only when its file is deleted). It needs confirm=true.
func Reset(ctx context.Context, sup Supervisor, specs []Spec, confirm bool) error {
	if !confirm {
		return errors.New("demorun: reset deletes the demo database, graph, manifest and logs; re-run with --yes")
	}
	if err := Down(ctx, sup, specs); err != nil {
		return fmt.Errorf("demorun: cannot reset while a service refuses to stop: %w", err)
	}
	l := sup.Layout
	steps := []struct {
		what string
		do   func() error
	}{
		{"postgres data", func() error { return embedded.WipePersistent(embedded.PersistentOptions{Dir: l.PGDir()}) }},
		{"neo4j data", func() error { return neo4jtest.WipePersistent(l.Neo4jDir()) }},
		{"replay events", func() error { return os.RemoveAll(l.ReplayEventsDir()) }},
		{"manifest", func() error { return removeIfExists(l.ManifestFile()) }},
		{"state", func() error { return removeIfExists(l.StateFile()) }},
		{"logs", func() error { return os.RemoveAll(l.LogDir()) }},
		{"pid files", func() error { return os.RemoveAll(l.PIDDir()) }},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			return fmt.Errorf("demorun: reset %s: %w", s.what, err)
		}
		sup.say("reset: removed %s", s.what)
	}
	return nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
