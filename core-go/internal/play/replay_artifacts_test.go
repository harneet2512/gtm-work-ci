package play

import (
	"encoding/json"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/proof"
)

// artifactsByName runs EpisodeArtifacts and returns the payload of each file by name.
func artifactsByName(t *testing.T, r *rig, runID string) map[string]map[string]any {
	t.Helper()
	arts, err := r.svc.EpisodeArtifacts(bg, r.manifest, runID)
	if err != nil {
		t.Fatalf("EpisodeArtifacts: %v", err)
	}
	out := make(map[string]map[string]any, len(arts))
	for _, a := range arts {
		payload, err := json.Marshal(a.Data)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(payload, &m); err != nil {
			t.Fatal(err)
		}
		if a.RunID != runID || a.Status == "" || len(a.RequirementIDs) == 0 {
			t.Fatalf("artifact envelope: %+v", a)
		}
		out[a.Artifact] = m
	}
	return out
}

func TestTheArtifactsAreTheReplayFromTheRealRows(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 3)

	// A produced file reports partial — the harness stamps confirmed, the emitter never does — and names
	// the rows it came from.
	docs, err := r.svc.EpisodeArtifacts(bg, r.manifest, "har129-run-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if d.Status != proof.StatusPartial || len(d.Evidence) == 0 {
			t.Fatalf("artifact %s: status %q evidence %v", d.Artifact, d.Status, d.Evidence)
		}
	}

	arts := artifactsByName(t, r, "har129-run-1")
	timeline, ok := arts["episode_timeline.json"]
	if !ok {
		t.Fatalf("no episode_timeline.json in %v", arts)
	}
	payload, _ := json.Marshal(timeline)
	validateDef(t, "episodeTimeline", payload)
	if timeline["total"] != 3.0 || timeline["released"] != 3.0 || len(timeline["episodes"].([]any)) != 3 {
		t.Fatalf("timeline: %v", timeline)
	}
	ep1 := timeline["episodes"].([]any)[0].(map[string]any)
	if ep1["released"] != true || ep1["material"] != true {
		t.Fatalf("timeline episode 1: %+v", ep1)
	}
	ep3 := timeline["episodes"].([]any)[2].(map[string]any)
	if ep3["held_out"] != true || ep3["material"] != true || ep3["account_change_id"] == nil {
		t.Fatalf("timeline episode 3: %+v", ep3)
	}

	snapshots, ok := arts["state_snapshots.json"]
	if !ok {
		t.Fatal("no state_snapshots.json")
	}
	payload, _ = json.Marshal(snapshots)
	validateDef(t, "stateSnapshots", payload)
	snaps := snapshots["snapshots"].([]any)
	if len(snaps) != 3 {
		t.Fatalf("snapshots = %v", snaps)
	}
	for i, s := range snaps {
		m := s.(map[string]any)
		if m["version"] != float64(i+1) || m["digest"] == nil || m["position"] != float64(i+1) {
			t.Fatalf("snapshot %d: %+v", i, m)
		}
	}

	diffs, ok := arts["graph_diffs.json"]
	if !ok {
		t.Fatal("no graph_diffs.json")
	}
	payload, _ = json.Marshal(diffs)
	validateDef(t, "graphDiffs", payload)
	ds := diffs["diffs"].([]any)
	if len(ds) != 3 {
		t.Fatalf("graph diffs = %v", ds)
	}
	for i, d := range ds {
		m := d.(map[string]any)
		if m["position"] != float64(i+1) || m["graph_diff_id"] == nil || m["job_id"] == nil {
			t.Fatalf("graph diff %d: %+v", i, m)
		}
	}
}

func TestTheArtifactsOfAnUnrunReplaySayNotRun(t *testing.T) {
	r := newEpisodeRig(t)
	if _, err := r.svc.EpisodeArtifacts(bg, r.manifest, "run-0"); err == nil {
		t.Fatal("a run id outside the har129- pattern must be refused")
	}
	arts, err := r.svc.EpisodeArtifacts(bg, r.manifest, "har129-run-0")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		if a.Status != proof.StatusNotRun {
			t.Fatalf("status of %s: %s", a.Artifact, a.Status)
		}
	}
	m := artifactsByName(t, r, "har129-run-0")
	timeline := m["episode_timeline.json"]
	payload, _ := json.Marshal(timeline)
	validateDef(t, "episodeTimeline", payload)
	if timeline["released"] != 0.0 || len(timeline["episodes"].([]any)) != 3 {
		t.Fatalf("unrun timeline: %v", timeline)
	}
	for _, s := range []string{"state_snapshots.json", "graph_diffs.json"} {
		payload, _ := json.Marshal(m[s])
		def := "stateSnapshots"
		if s == "graph_diffs.json" {
			def = "graphDiffs"
		}
		validateDef(t, def, payload)
	}
}
