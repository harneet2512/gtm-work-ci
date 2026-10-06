package evalreport

// An in-memory world for the pure-computation tests (no database): episodes, deltas and eval rows are
// assembled directly so every category / edge case of the metric functions can be driven in
// milliseconds. The database-backed tests in report_test.go prove the loaders and the real write paths.

import (
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/learning"
)

type fixture struct {
	w *world
	n int
}

func newFixture() *fixture {
	return &fixture{w: &world{
		versions: map[string]*versionRow{}, episodes: map[string]*episodeRow{},
		byRun: map[string]*episodeRow{}, anyByRun: map[string]*episodeRow{},
		deltas: map[string]*deltaRow{}, explains: map[string][]explRow{},
		candidates: map[string]map[int]draftJSON{}, decisions: map[string]*hsdRow{},
	}}
}

// episode adds a decided episode on the final draft index 1. delta may be nil (no linked delta).
func (f *fixture) episode(action string, d *deltaRow) *episodeRow {
	f.n++
	ep := &episodeRow{ID: fmt.Sprintf("ep%02d", f.n), RunID: fmt.Sprintf("run%02d", f.n), AccountID: "acct",
		Status: "decided", FinalDraftIndex: 1, HumanAction: action, HasDecision: true}
	if d != nil {
		d.EpisodeID = ep.ID
		if d.ID == "" {
			d.ID = "delta-" + ep.ID
		}
		ep.HumanDeltaID = d.ID
		f.w.deltas[d.ID] = d
	}
	f.w.episodes[ep.ID] = ep
	f.w.byRun[ep.RunID] = ep
	f.w.anyByRun[ep.RunID] = ep
	return ep
}

// rows attaches eval rows of (evaluator, tag) with the given verdicts to the episode and returns them
// keyed the way evalsByTag does.
func (f *fixture) rows(ep *episodeRow, evaluator, tag string, verdicts ...string) []*evalRow {
	var out []*evalRow
	for i, v := range verdicts {
		r := &evalRow{ID: fmt.Sprintf("%s-%s-%d", ep.ID, tag, i), RunID: ep.RunID, DraftIndex: ep.FinalDraftIndex,
			Evaluator: evaluator, Tag: tag, Kind: "semantic", Verdict: v}
		f.w.finalEvals = append(f.w.finalEvals, r)
		f.w.allEvals = append(f.w.allEvals, r)
		out = append(out, r)
	}
	return out
}

func (f *fixture) byEpisode(tag string) map[string][]*evalRow {
	by, _ := f.w.evalsByTag(tag)
	return by
}

func deltaOfKind(kinds ...string) *deltaRow {
	d := &deltaRow{}
	for _, k := range kinds {
		d.Changes = append(d.Changes, learning.LiteralChange{Kind: k})
	}
	return d
}
