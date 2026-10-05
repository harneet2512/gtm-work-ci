package contracttest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// TestRunRecomputationConformsToTheContract exercises GET /runs/{run_id}/recomputation end to end: a seeded
// episode is chosen with a recipient edit and sent over HTTP, and every answer on the way (nothing chosen, edits
// saved, sent) is checked against core.yaml and against what the stored facts prove.
func TestRunRecomputationConformsToTheContract(t *testing.T) {
	s := newStack(t)
	seed := strategytest.Seed(t, env.DB, s.world.AccountA)
	run := "/runs/" + seed.RunID
	path := "/runs/{run_id}/recomputation"

	type span struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
	}
	type doc struct {
		Status  string `json:"status"`
		Edited  bool   `json:"edited"`
		Entries []struct {
			Edit struct {
				Field string `json:"field"`
				Kind  string `json:"kind"`
			} `json:"edit"`
			Invalidated   []span `json:"invalidated"`
			Recomputed    []span `json:"recomputed"`
			NotRecomputed []span `json:"not_recomputed"`
		} `json:"entries"`
		AccountState struct {
			Preserved *bool `json:"preserved"`
		} `json:"account_state"`
	}
	read := func() doc {
		r := s.get(run+"/recomputation", path)
		if r.status != http.StatusOK {
			t.Fatalf("recomputation: %d %s", r.status, clip(r.body))
		}
		var d doc
		if err := json.Unmarshal(r.body, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}

	if d := read(); d.Status != "not_decided" || len(d.Entries) != 0 || d.AccountState.Preserved != nil {
		t.Fatalf("nothing chosen: %+v", d)
	}

	choose := map[string]any{"selected_candidate_id": seed.Candidates[1], "surface": "api", "actor_label": "op"}
	if r := s.post(run+"/strategy-decision", "/runs/{run_id}/strategy-decision", choose); r.status != 201 {
		t.Fatalf("choose: %d %s", r.status, clip(r.body))
	}
	if d := read(); d.Status != "unedited" || d.Edited || len(d.Entries) != 0 {
		t.Fatalf("chosen with no edit: %+v", d)
	}

	choose["final_cc"] = []any{} // remove Priya from cc
	if r := s.post(run+"/strategy-decision", "/runs/{run_id}/strategy-decision", choose); r.status != 200 {
		t.Fatalf("edit: %d %s", r.status, clip(r.body))
	}
	d := read()
	if d.Status != "edits_pending" || len(d.Entries) != 1 || len(d.Entries[0].Recomputed) != 0 {
		t.Fatalf("edits saved, not sent: %+v", d)
	}

	send := map[string]any{"decision": "send", "surface": "api", "actor_label": "op"}
	if r := s.post(run+"/send", "/runs/{run_id}/send", send); r.status != 200 {
		t.Fatalf("send: %d %s", r.status, clip(r.body))
	}
	d = read()
	if d.Status != "reevaluated" || !d.Edited || len(d.Entries) != 1 {
		t.Fatalf("sent with an edit: %+v", d)
	}
	e := d.Entries[0]
	if e.Edit.Field != "recipients" || e.Edit.Kind != "recipient_removed" || d.AccountState.Preserved == nil || !*d.AccountState.Preserved {
		t.Fatalf("entry = %+v", e)
	}
	hasKind := func(spans []span, kind string) bool {
		for _, sp := range spans {
			if sp.Kind == kind {
				return true
			}
		}
		return false
	}
	if !hasKind(e.Invalidated, "artifact_field") || !hasKind(e.Invalidated, "eval_result") || !hasKind(e.Recomputed, "final_artifact") || !hasKind(e.NotRecomputed, "ranking_rationale") {
		t.Errorf("invalidated %v recomputed %v not_recomputed %v", e.Invalidated, e.Recomputed, e.NotRecomputed)
	}

	if r := s.get("/runs/"+missingID+"/recomputation", path); r.status != http.StatusNotFound {
		t.Fatalf("unknown run: %d %s", r.status, clip(r.body))
	}
	if r := s.do("GET", run+"/recomputation", path, "", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("no token: %d", r.status)
	}
}
