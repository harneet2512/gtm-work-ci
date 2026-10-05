package play

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/proof"
)

// har129RunID is har129_proof.v1.json's run_id pattern; the emitter refuses ids the artifact schema would
// reject downstream.
var har129RunID = regexp.MustCompile(`^har129-[A-Za-z0-9._-]+$`)

// EpisodeArtifacts emits the three §H payloads of a manifest's replay from the authoritative stores: the
// timeline of its sequence, the state snapshots at each released episode and the graph-projection diffs the
// released events caused. Nothing is fabricated: the payload is the bookkeeping and the world-timed state
// reads, the evidence lists the rows it came from, and the status is partial once episodes are released —
// the harness alone stamps confirmed when the live run supplies the file.
func (s *Service) EpisodeArtifacts(ctx context.Context, manifestID, runID string) ([]proof.ArtifactDoc, error) {
	if !har129RunID.MatchString(runID) {
		return nil, fmt.Errorf("play: run id %q does not match %s", runID, har129RunID)
	}
	seq, err := LoadSequence(ctx, s.db, manifestID)
	if err != nil {
		return nil, err
	}
	rows, err := loadEpisodes(ctx, s.db, manifestID)
	if err != nil {
		return nil, err
	}
	released, err := releasedCursor(ctx, s.db, manifestID)
	if err != nil {
		return nil, err
	}
	at := s.clk.Now().UTC().Format(time.RFC3339)
	status := proof.StatusNotRun
	if released > 0 {
		status = proof.StatusPartial
	}
	timeline, err := s.episodeTimeline(ctx, seq, released, rows)
	if err != nil {
		return nil, err
	}
	snapshots, err := s.stateSnapshots(ctx, seq, rows)
	if err != nil {
		return nil, err
	}
	diffs, err := s.graphDiffs(ctx, seq, rows)
	if err != nil {
		return nil, err
	}
	doc := func(name string, ids []string, data map[string]any) proof.ArtifactDoc {
		return proof.ArtifactDoc{RunID: runID, Artifact: name, GeneratedAt: at, Status: status,
			RequirementIDs: ids, Evidence: evidenceRefs(rows), Data: data}
	}
	return []proof.ArtifactDoc{
		doc("episode_timeline.json", []string{"HAR129-SB-01", "HAR129-G-01"}, timeline),
		doc("state_snapshots.json", []string{"HAR129-SB-02", "HAR129-G-03"}, snapshots),
		doc("graph_diffs.json", []string{"HAR129-G-04"}, diffs),
	}, nil
}

// evidenceRefs names the rows the payloads were read from, so a PASS can be traced to machine-checkable
// stores (SH-01): the demo_episodes bookkeeping positions and the graph_projection_diffs ids they name.
func evidenceRefs(rows []episodeRow) []string {
	refs := make([]string, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, fmt.Sprintf("demo_episodes:position=%d", r.Position))
		if r.GraphDiffID != nil {
			refs = append(refs, fmt.Sprintf("graph_projection_diffs:id=%d", *r.GraphDiffID))
		}
	}
	return refs
}

// episodeTimeline is the payload of episode_timeline.json ($defs/episodeTimeline): every event of the
// sequence, released flag and bookkeeping where released.
func (s *Service) episodeTimeline(ctx context.Context, seq Sequence, released int, rows []episodeRow) (map[string]any, error) {
	byPosition := make(map[int]episodeRow, len(rows))
	for _, r := range rows {
		byPosition[r.Position] = r
	}
	episodes := make([]episodeEventJSON, 0, seq.N())
	for pos := 1; pos <= seq.N(); pos++ {
		ev, _ := seq.At(pos)
		var row *episodeRow
		if r, ok := byPosition[pos]; ok {
			row = &r
		}
		episodes = append(episodes, eventJSON(ev, row))
	}
	return map[string]any{
		"manifest_id":  seq.ManifestID,
		"account_id":   seq.AccountID,
		"total":        seq.N(),
		"released":     released,
		"episodes":     episodes,
		"generated_at": s.clk.Now().UTC(),
	}, nil
}

type snapshotJSON struct {
	Position int       `json:"position"`
	EventID  string    `json:"event_id"`
	Version  int       `json:"version"`
	AsOf     time.Time `json:"as_of"`
	Digest   string    `json:"digest"`
}

// stateSnapshots is the payload of state_snapshots.json ($defs/stateSnapshots): one snapshot per released
// episode whose world-timed state exists.
func (s *Service) stateSnapshots(ctx context.Context, seq Sequence, rows []episodeRow) (map[string]any, error) {
	snapshots := make([]snapshotJSON, 0, len(rows))
	for _, r := range rows {
		st, err := s.stateAt(ctx, seq.AccountID, r.Position, episodeBound(seq, r.Position))
		if err != nil {
			return nil, err
		}
		if st == nil {
			continue
		}
		snapshots = append(snapshots, snapshotJSON{Position: r.Position, EventID: r.EventID,
			Version: st.Version, AsOf: *st.AsOf, Digest: st.Digest})
	}
	return map[string]any{
		"manifest_id":  seq.ManifestID,
		"account_id":   seq.AccountID,
		"snapshots":    snapshots,
		"generated_at": s.clk.Now().UTC(),
	}, nil
}

type graphDiffJSON struct {
	Position    int             `json:"position"`
	EventID     string          `json:"event_id"`
	GraphDiffID int64           `json:"graph_diff_id"`
	JobID       int64           `json:"job_id"`
	Summary     json.RawMessage `json:"summary"`
	Changes     json.RawMessage `json:"changes"`
}

// graphDiffs is the payload of graph_diffs.json ($defs/graphDiffs): every projection diff the released
// events' stored source events appear in — the real projector's rows, not a reconstruction.
func (s *Service) graphDiffs(ctx context.Context, seq Sequence, rows []episodeRow) (map[string]any, error) {
	if len(rows) == 0 {
		return map[string]any{"manifest_id": seq.ManifestID, "account_id": seq.AccountID,
			"diffs": []graphDiffJSON{}, "generated_at": s.clk.Now().UTC()}, nil
	}
	rrows, err := s.db.QueryContext(ctx, `SELECT e.position, e.event_id::text, g.id, g.job_id, g.summary::text, g.changes::text
	  FROM demo_episodes e JOIN graph_projection_diffs g ON e.source_event_id = ANY(g.source_event_ids)
	  WHERE e.manifest_id = $1::uuid ORDER BY e.position, g.id`, seq.ManifestID)
	if err != nil {
		return nil, fmt.Errorf("play: read the graph diffs of %s: %w", seq.ManifestID, err)
	}
	defer rrows.Close()
	diffs := make([]graphDiffJSON, 0)
	for rrows.Next() {
		var d graphDiffJSON
		var summary, changes []byte
		if err := rrows.Scan(&d.Position, &d.EventID, &d.GraphDiffID, &d.JobID, &summary, &changes); err != nil {
			return nil, fmt.Errorf("play: scan a graph diff of %s: %w", seq.ManifestID, err)
		}
		d.Summary, d.Changes = summary, changes
		diffs = append(diffs, d)
	}
	if err := rrows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"manifest_id":  seq.ManifestID,
		"account_id":   seq.AccountID,
		"diffs":        diffs,
		"generated_at": s.clk.Now().UTC(),
	}, nil
}
