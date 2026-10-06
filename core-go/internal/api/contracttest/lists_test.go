package contracttest

import (
	"encoding/json"
	"net/url"
	"testing"
)

type listPage struct {
	Items []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func decodeList(t *testing.T, r reply) listPage {
	t.Helper()
	if r.status != 200 {
		t.Fatalf("status %d: %s", r.status, clip(r.body))
	}
	var p listPage
	if err := json.Unmarshal(r.body, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestListEndpointsConformToTheContract walks GET /accounts and GET /runs (HAR-145) over HTTP against the sample
// world: pagination to the end, the run filters, and every refusal, each response checked against core.yaml.
func TestListEndpointsConformToTheContract(t *testing.T) {
	s := newStack(t)
	accounts, runs := "/accounts", "/runs"

	first := decodeList(t, s.get("/accounts?limit=1", accounts))
	if len(first.Items) != 1 {
		t.Fatalf("limit=1 returned %d accounts", len(first.Items))
	}
	if first.NextCursor != nil {
		page2 := decodeList(t, s.get("/accounts?limit=1&cursor="+url.QueryEscape(*first.NextCursor), accounts))
		if len(page2.Items) != 1 || page2.Items[0].ID == first.Items[0].ID {
			t.Fatalf("the second page repeats the first: %+v", page2.Items)
		}
	}
	whole := decodeList(t, s.get("/accounts", accounts))
	if whole.NextCursor != nil && len(whole.Items) != 50 {
		t.Fatalf("a short page must not carry a cursor: %d items", len(whole.Items))
	}

	byAccount := decodeList(t, s.get("/runs?account_id="+s.world.AccountA, runs))
	if len(byAccount.Items) == 0 {
		t.Fatal("the sample world's account A has runs")
	}
	found := false
	for _, it := range byAccount.Items {
		found = found || it.ID == s.world.RunA
	}
	if !found {
		t.Fatalf("run A is not among account A's runs: %+v", byAccount.Items)
	}
	if byStatus := decodeList(t, s.get("/runs?status=failed&limit=200", runs)); len(byStatus.Items) > 0 {
		for _, it := range byStatus.Items {
			if it.Status != "failed" {
				t.Fatalf("status filter leaked %s", it.Status)
			}
		}
	}
	if empty := decodeList(t, s.get("/runs?account_id="+missingID, runs)); len(empty.Items) != 0 || empty.NextCursor != nil {
		t.Fatalf("an unknown account has no runs: %+v", empty)
	}
	oneRun := decodeList(t, s.get("/runs?limit=1", runs))
	if len(oneRun.Items) != 1 {
		t.Fatalf("limit=1 returned %d runs", len(oneRun.Items))
	}

	for _, bad := range []string{"/accounts?limit=0", "/accounts?limit=201", "/accounts?cursor=%25%25", "/runs?status=sleeping",
		"/runs?account_id=not-a-uuid", "/runs?limit=abc", "/runs?cursor=Zm9yZWlnbg"} {
		tmpl := accounts
		if bad[:5] == "/runs" {
			tmpl = runs
		}
		if r := s.get(bad, tmpl); r.status != 400 || errorCode(t, r) != "bad_request" {
			t.Errorf("%s: %d %s", bad, r.status, clip(r.body))
		}
	}
	if r := s.do("GET", "/accounts", accounts, "", nil); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
	if r := s.do("GET", "/runs", runs, "", nil); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
	if r := s.do("POST", "/runs", "", apiToken, nil); r.status != 405 {
		t.Fatalf("POST /runs: %d", r.status)
	}
}
