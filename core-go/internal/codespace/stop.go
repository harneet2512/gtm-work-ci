package codespace

import (
	"context"
	"errors"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// StopAll is the Stop shortcut: it stops every supervised service and then kills any Neo4j JVM still running under a Neo4j directory.
// Stopping only the recorded PIDs leaves a JVM that outlived its launcher holding the database and its port, which breaks the
// next Start. Both steps always run; their errors are joined.
func StopAll(ctx context.Context, down func(context.Context) error, list func(context.Context) ([]demorun.Process, error),
	kill func(pid int) error, neo4jDirs []string, say func(format string, args ...any)) error {
	downErr := down(ctx)
	var sweepErrs []error
	for _, dir := range neo4jDirs { // one Neo4j instance per case
		n, err := demorun.SweepOrphans(ctx, list, kill, dir)
		sweepErrs = append(sweepErrs, err)
		if n > 0 && say != nil {
			say("stopped %d orphaned Neo4j JVM(s) under %s", n, dir)
		}
	}
	return errors.Join(append([]error{downErr}, sweepErrs...)...)
}
