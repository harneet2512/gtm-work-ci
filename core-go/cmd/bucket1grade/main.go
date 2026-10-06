// Command bucket1grade grades Bucket 1 (B1-B9) on one episode bundle and merges the results into the artifact
// the web evals page reads. It never calls a model: semantic judgments are replayed from a cassette file.
//
//	bucket1grade -episode bundle.json -judgments judgments.json -out results.json
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "bucket1grade:", err)
		os.Exit(1)
	}
}

func run(args []string, out *os.File) error {
	fs := flag.NewFlagSet("bucket1grade", flag.ContinueOnError)
	bundle := fs.String("episode", "", "episode bundle JSON (required)")
	judgments := fs.String("judgments", "", "recorded model judgments (cassette); absent means not measured")
	dest := fs.String("out", "", "results artifact to merge into (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bundle == "" || *dest == "" {
		return fmt.Errorf("-episode and -out are required")
	}
	ep, err := bucket1.ReadEpisode(*bundle)
	if err != nil {
		return err
	}
	var js map[string]bucket1.Judgment
	if *judgments != "" {
		if js, err = bucket1.LoadJudgments(*judgments, ep.ID); err != nil {
			return err
		}
	}
	results := bucket1.Run(ep, js)
	if err := bucket1.WriteResults(*dest, ep.ID, results); err != nil {
		return err
	}
	for _, r := range results {
		fmt.Fprintf(out, "%s %-7s %s\n", r.Gate, r.Verdict, r.Why)
	}
	return nil
}
