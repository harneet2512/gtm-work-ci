package contracttest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
)

// TestRunProgressConformsToTheContract exercises GET /runs/{run_id}/progress and GET
// /replay/manifests/{manifest_id}/progress over HTTP against a real database: a run that never came from a Play
// reports its upstream stages as unknown (never passed), a stage the pipeline recorded shows with its refs, and
// an unknown run or manifest is a 404 in the contract's envelope.
func TestRunProgressConformsToTheContract(t *testing.T) {
	s := newStack(t)
	account := s.world.AccountB // account B's run never came from a Play
	runID := s.world.RunB
	t.Cleanup(func() { _, _ = env.DB.Exec(`DELETE FROM pipeline_stage_events WHERE account_id = $1::uuid`, account) })
	path := "/runs/{run_id}/progress"

	type doc struct {
		Scope   string  `json:"scope"`
		RunID   *string `json:"run_id"`
		Overall string  `json:"overall"`
		Stages  []struct {
			Stage  string `json:"stage"`
			Status string `json:"status"`
			Detail string `json:"detail"`
			Refs   struct {
				RunID string `json:"run_id"`
			} `json:"refs"`
		} `json:"stages"`
	}
	read := func() doc {
		r := s.get("/runs/"+runID+"/progress", path)
		if r.status != http.StatusOK {
			t.Fatalf("progress: %d %s", r.status, clip(r.body))
		}
		if r.header.Get("Cache-Control") != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", r.header.Get("Cache-Control"))
		}
		var d doc
		if err := json.Unmarshal(r.body, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}

	before := read()
	if before.Scope != "run" || before.RunID == nil || *before.RunID != runID || len(before.Stages) != 7 {
		t.Fatalf("progress = %+v", before)
	}
	for _, st := range before.Stages {
		switch st.Stage {
		case "ingest", "resolve", "graph", "state":
			if st.Status != "unknown" || st.Detail == "" {
				t.Errorf("%s = %s %q: the pipeline did not record it, so it is unknown with a reason, never passed", st.Stage, st.Status, st.Detail)
			}
		default:
			if st.Status != "waiting" {
				t.Errorf("%s = %s, want waiting", st.Stage, st.Status)
			}
		}
	}

	rec, err := stageevents.NewRecorder(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Record(context.Background(), stageevents.Scope{RunID: runID, AccountID: account}, stageevents.Decide, stageevents.Completed,
		stageevents.Outcome{Refs: stageevents.Refs{RunID: runID}, Detail: "strategy set published"}); err != nil {
		t.Fatal(err)
	}
	after := read()
	var decide string
	for _, st := range after.Stages {
		if st.Stage == "decide" {
			decide = st.Status
			if st.Refs.RunID != runID {
				t.Errorf("decide refs = %+v", st.Refs)
			}
		}
	}
	if decide != "completed" {
		t.Fatalf("decide = %s after the run decided", decide)
	}

	if r := s.get("/runs/"+missingID+"/progress", path); r.status != http.StatusNotFound {
		t.Fatalf("unknown run: %d %s", r.status, clip(r.body))
	}
	if r := s.do("GET", "/runs/"+runID+"/progress", path, "", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("no token: %d", r.status)
	}
	if r := s.get("/replay/manifests/"+missingID+"/progress", "/replay/manifests/{manifest_id}/progress"); r.status != http.StatusNotFound {
		t.Fatalf("unknown manifest: %d %s", r.status, clip(r.body))
	}
}
