package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
)

// coveredFile is the synthetic layer's list of deals whose stage path and outcome it supplies
// (WP32, HAR-131; written by the generator as labels/covered_deals.json).
type coveredFile struct {
	Provenance string   `json:"provenance"`
	Deals      []string `json:"deals"`
}

// readCovered loads the covered deals of --synthetic-covered.
func readCovered(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ghostctl: read --synthetic-covered: %w", err)
	}
	var f coveredFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("ghostctl: decode --synthetic-covered %s: %w", path, err)
	}
	if f.Provenance == "" || len(f.Deals) == 0 {
		return nil, fmt.Errorf("ghostctl: --synthetic-covered %s names no provenance or no deals", path)
	}
	covered := make(map[string]bool, len(f.Deals))
	for _, d := range f.Deals {
		covered[d] = true
	}
	return covered, nil
}

// supersede applies open question 2 (decided 2026-10-02): the synthetic stage path replaces this
// loader's snapshot StageName event for the covered deals; a deal with a visible real outcome is refused.
func supersede(res crmarena.Result, path string, out io.Writer) (crmarena.Result, error) {
	if path == "" {
		return res, nil
	}
	covered, err := readCovered(path)
	if err != nil {
		return res, err
	}
	kept, err := crmarena.SupersedeSnapshotStage(res.Events, covered)
	if err != nil {
		return res, err
	}
	fmt.Fprintf(out, "synthetic layer covers %d deals: %d snapshot stage events superseded\n", len(covered), len(res.Events)-len(kept))
	res.Events = kept
	return res, nil
}
