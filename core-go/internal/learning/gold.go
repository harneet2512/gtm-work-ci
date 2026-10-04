package learning

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
)

// goldCase is the part of a fixtures/evals case a criterion backtest reads: its expected eval
// assertions and the candidate action the axis checks can replay.
type goldCase struct {
	ID         string          `json:"id"`
	ShouldPass bool            `json:"should_pass"`
	Candidate  json.RawMessage `json:"candidate_action"`
	Expected   []struct {
		EvalType string `json:"eval_type"`
		Verdict  string `json:"verdict"`
	} `json:"expected"`
}

// loadGoldCases reads every case file under the given directories (fixtures/evals/cases and
// fixtures/evals/crmarena share the shape; a missing directory is skipped, never fatal).
func loadGoldCases(dirs []string) ([]goldCase, error) {
	var files []string
	for _, dir := range dirs {
		fs, err := filepath.Glob(filepath.Join(dir, "*", "*.json"))
		if err != nil {
			return nil, fmt.Errorf("learning: glob %s: %w", dir, err)
		}
		files = append(files, fs...)
	}
	sort.Strings(files)
	var out []goldCase
	for _, fn := range files {
		raw, err := os.ReadFile(fn)
		if err != nil {
			return nil, fmt.Errorf("learning: read gold case %s: %w", fn, err)
		}
		var c goldCase
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("learning: decode gold case %s: %w", fn, err)
		}
		if filepath.Base(fn) == "case.schema.json" || c.ID == "" {
			continue // schema and manifest files are not cases
		}
		out = append(out, c)
	}
	return out, nil
}

// expectedVerdict is the verdict the gold asserts for the axis on this case ("" = the case does not
// assert the axis).
func (c goldCase) expectedVerdict(axis string) string {
	for _, e := range c.Expected {
		if e.EvalType == axis {
			return e.Verdict
		}
	}
	return ""
}

// candidate decodes the case's agent_run_output candidate for the axis checks.
func (c goldCase) candidate() (deterministic.Output, error) {
	var out deterministic.Output
	if err := json.Unmarshal(c.Candidate, &out); err != nil {
		return out, fmt.Errorf("learning: candidate of gold case %s: %w", c.ID, err)
	}
	return out, nil
}
