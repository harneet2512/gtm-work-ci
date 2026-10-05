package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/harneet2512/gtm-work/core-go/internal/proof"
)

const proofHar129Usage = "usage: ghostctl proof-har129 [--out <dir>] [--run-id <id>] [--requirements <path>]"

// runProofHar129 writes the HAR-129 agent acceptance artifact (section H) and the confirmation
// matrix (section I) to artifacts/har129/<run_id>/. It needs no database: it emits the skeleton with
// every requirement NOT RUN until the integrated run fills the evidence. It never fabricates
// evidence.
func runProofHar129(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("proof-har129", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	outDir := fs.String("out", "", "base output directory (default <repo>/artifacts/har129)")
	runID := fs.String("run-id", "", "run id (default har129-<UTC timestamp>-<random>)")
	reqPath := fs.String("requirements", "", "requirement source of truth (default "+proof.RequirementsPath+")")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New(proofHar129Usage)
	}
	m, dir, err := proof.Run(proof.Options{OutDir: *outDir, RunID: *runID, RequirementsPath: *reqPath})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "har129 proof run %s\n", m.RunID)
	fmt.Fprintf(out, "run directory: %s\n", dir)
	fmt.Fprintf(out, "status: %s\n", m.Status.Display())
	fmt.Fprintf(out, "requirements: %d (CONFIRMED %d, PARTIAL %d, NOT CONFIRMED %d, FAILED %d, NOT RUN %d)\n",
		m.Summary.Total, m.Summary.Confirmed, m.Summary.Partial, m.Summary.NotConfirmed, m.Summary.Failed, m.Summary.NotRun)
	fmt.Fprintf(out, "artifacts: %d files (manifest.json and demo-report.md included)\n", len(m.Artifacts))
	fmt.Fprintln(out, "no integrated run was performed: every matrix row is NOT RUN until a real run supplies evidence.")
	return nil
}
