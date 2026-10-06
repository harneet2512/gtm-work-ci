package contracttest

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evaldispute/disputetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

func seedResult(t *testing.T, runID string, draft int, evalType, verdict string, blocking bool) string {
	t.Helper()
	return disputetest.SeedResult(t, env.DB, disputetest.Result{RunID: runID, DraftIndex: draft, EvalType: evalType, Verdict: verdict, Blocking: blocking})
}

type evalRunBody struct {
	ID                string  `json:"id"`
	PreviousEvalRunID *string `json:"previous_eval_run_id"`
	ResultCount       int     `json:"result_count"`
	Counts            struct {
		Pass, Warn, Fail, Unknown, Total int
	} `json:"counts"`
}

// TestEvalRunEndpointsConformToTheContract walks GET /eval-runs, /eval-runs/{id}/families and
// /eval-runs/compare over HTTP against two seeded runs of one account: counts, the previous-run delta, the per-type
// change and every refusal, each response checked against core.yaml and the eval_run schemas.
func TestEvalRunEndpointsConformToTheContract(t *testing.T) {
	s := newStack(t)
	acct := s.world.AccountA
	first := strategytest.Seed(t, env.DB, acct)
	seedResult(t, first.RunID, 1, "champion_continuity", "fail", true)
	seedResult(t, first.RunID, 1, "cta_calibration", "warn", false)
	second := strategytest.Seed(t, env.DB, acct)
	seedResult(t, second.RunID, 1, "champion_continuity", "pass", false)
	seedResult(t, second.RunID, 1, "cta_calibration", "warn", false)
	seedResult(t, second.RunID, 2, "provenance_coverage", "abstain", false)

	list, fam, cmp := "/eval-runs", "/eval-runs/{eval_run_id}/families", "/eval-runs/compare"

	r := s.get("/eval-runs?account_id="+acct+"&limit=1", list)
	var page struct {
		Items      []evalRunBody `json:"items"`
		NextCursor *string       `json:"next_cursor"`
	}
	if r.status != 200 || json.Unmarshal(r.body, &page) != nil || len(page.Items) != 1 || page.Items[0].ID != second.RunID || page.NextCursor == nil {
		t.Fatalf("first page: %d %s", r.status, clip(r.body))
	}
	if page.Items[0].PreviousEvalRunID == nil || *page.Items[0].PreviousEvalRunID != first.RunID || page.Items[0].Counts.Unknown != 1 || page.Items[0].ResultCount != 3 {
		t.Fatalf("the newest run: %+v", page.Items[0])
	}
	r = s.get("/eval-runs?account_id="+acct+"&limit=1&cursor="+url.QueryEscape(*page.NextCursor), list)
	if r.status != 200 || json.Unmarshal(r.body, &page) != nil || len(page.Items) != 1 || page.Items[0].ID != first.RunID {
		t.Fatalf("second page: %d %s", r.status, clip(r.body))
	}

	r = s.get("/eval-runs/"+second.RunID+"/families", fam)
	if r.status != 200 {
		t.Fatalf("families: %d %s", r.status, clip(r.body))
	}
	var sum struct {
		Areas []struct {
			Area     string `json:"area"`
			Measured bool   `json:"measured"`
		} `json:"areas"`
	}
	if err := json.Unmarshal(r.body, &sum); err != nil || len(sum.Areas) != 4 || sum.Areas[3].Area != "system" || sum.Areas[3].Measured {
		t.Fatalf("families body: %v %s", err, clip(r.body))
	}

	r = s.get("/eval-runs/compare?a="+first.RunID+"&b="+second.RunID, cmp)
	var c struct {
		Overall struct {
			Change string `json:"change"`
		} `json:"overall"`
		Rows []struct {
			EvalType string `json:"eval_type"`
			Change   string `json:"change"`
		} `json:"rows"`
	}
	if r.status != 200 || json.Unmarshal(r.body, &c) != nil || c.Overall.Change != "improved" || len(c.Rows) != 3 {
		t.Fatalf("compare: %d %s", r.status, clip(r.body))
	}

	// Refusals.
	s.expectError(s.get("/eval-runs/"+missingID+"/families", fam), 404, "not_found")
	s.expectError(s.get("/eval-runs/compare?a="+first.RunID+"&b="+missingID, cmp), 404, "not_found")
	for _, bad := range []string{"/eval-runs/compare", "/eval-runs/compare?a=" + first.RunID, "/eval-runs/compare?a=x&b=y"} {
		s.expectError(s.get(bad, cmp), 400, "bad_request")
	}
	for _, bad := range []string{"/eval-runs?limit=0", "/eval-runs?account_id=nope", "/eval-runs?cursor=Zm9yZWlnbg"} {
		s.expectError(s.get(bad, list), 400, "bad_request")
	}
	if r := s.do("GET", "/eval-runs", list, "", nil); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
	if r := s.do("POST", "/eval-runs/compare", "", apiToken, nil); r.status != 405 {
		t.Fatalf("POST compare: %d", r.status)
	}
}
