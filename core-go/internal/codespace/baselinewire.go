package codespace

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// CacheName is the model-call cache directory under the demo home. It is outside the live copy and the baseline: every model
// answer is made once, stored, and never deleted or reset.
const CacheName = "cassettes"

// NewBaseline is the baseline of a demo configuration: the state the demo mutates, and nothing else. That is the Postgres
// cluster, each case's Neo4j data and transaction logs, the per-case files (frozen manifest, seed state, graph marker), the
// replay events and the active-case marker. Logs, PID files, binaries, secrets and the model-call cache are not part of it.
func NewBaseline(cfg demorun.Config, cases []Case) Baseline {
	home := cfg.Layout.Dir()
	rel := func(p string) string {
		r, err := filepath.Rel(home, p)
		if err != nil {
			return filepath.ToSlash(p)
		}
		return filepath.ToSlash(r)
	}
	paths := []string{rel(filepath.Join(cfg.Layout.PGDir(), "data"))}
	for _, dir := range cfg.GraphDirs() {
		paths = append(paths, rel(filepath.Join(dir, "data")), rel(filepath.Join(dir, "tx")))
	}
	p := Paths{Layout: cfg.Layout}
	casesRel := rel(filepath.Join(cfg.Layout.LiveDir(), "cases"))
	paths = append(paths, casesRel, rel(cfg.Layout.ReplayEventsDir()), rel(p.ActiveFile()))
	slots := make([]string, 0, len(cases))
	for _, c := range cases {
		paths = append(paths, rel(p.GraphMarker(c.Slot)))
		slots = append(slots, c.Slot)
	}
	return Baseline{Home: home, Paths: paths, Cases: slots, CasesRel: casesRel}
}

// SealRecording finishes the record run: the presenter's run sheet joins the baseline (so a Reset brings it back) and the size
// of the model-call cache is noted in the manifest. The run sheet must exist; the cache is noted, never copied.
func (r Runtime) SealRecording(ctx context.Context) error {
	if err := r.Baseline.Include(ctx, RunSheetName); err != nil {
		return fmt.Errorf("codespace: add the run sheet to the baseline: %w", err)
	}
	return r.Baseline.NoteCache(CacheName)
}
