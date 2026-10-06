package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// listReads records what the list handlers pass to the read service and answers fixed pages.
type listReads struct {
	stubReads
	gotLimit  int
	gotCursor string
	gotFilter readmodel.RunFilter
}

func (s *listReads) ListAccounts(_ context.Context, limit int, cursor string) (readmodel.AccountPage, error) {
	s.gotLimit, s.gotCursor = limit, cursor
	next := "next-token"
	return readmodel.AccountPage{Items: []readmodel.Account{{ID: testRun, Name: "Acme"}}, NextCursor: &next}, nil
}

func (s *listReads) ListRuns(_ context.Context, f readmodel.RunFilter) (readmodel.RunPage, error) {
	s.gotFilter = f
	return readmodel.RunPage{Items: []readmodel.Run{}}, nil
}

func TestListAccountsPassesLimitAndCursorAndKeepsTheNextCursor(t *testing.T) {
	reads := &listReads{}
	h, err := NewHandler(&fakeService{}, testToken, nil, WithReads(reads))
	if err != nil {
		t.Fatal(err)
	}
	rec := do(h, http.MethodGet, "/accounts?limit=7&cursor=abc", "", authed())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if reads.gotLimit != 7 || reads.gotCursor != "abc" {
		t.Fatalf("handler passed limit=%d cursor=%q", reads.gotLimit, reads.gotCursor)
	}
	var got struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Items) != 1 || got.NextCursor != "next-token" {
		t.Fatalf("page = %+v %v", got, err)
	}
}

func TestListRunsPassesTheFilters(t *testing.T) {
	reads := &listReads{}
	h, err := NewHandler(&fakeService{}, testToken, nil, WithReads(reads))
	if err != nil {
		t.Fatal(err)
	}
	rec := do(h, http.MethodGet, "/runs?account_id="+testRun+"&status=failed&limit=3&cursor=c1", "", authed())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	want := readmodel.RunFilter{AccountID: testRun, Status: "failed", Limit: 3, Cursor: "c1"}
	if reads.gotFilter != want {
		t.Fatalf("filter = %+v, want %+v", reads.gotFilter, want)
	}
	if !strings.Contains(rec.Body.String(), `"next_cursor":null`) {
		t.Fatalf("the last page carries an explicit null cursor: %s", rec.Body.String())
	}
}

func TestListsRefuseBadLimitsAndOversizedCursors(t *testing.T) {
	h, err := NewHandler(&fakeService{}, testToken, nil, WithReads(&listReads{}))
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("a", maxCursorLen+1)
	for _, path := range []string{"/accounts?limit=0", "/runs?limit=999", "/accounts?cursor=" + long, "/runs?cursor=" + long} {
		rec := do(h, http.MethodGet, path, "", authed())
		if rec.Code != http.StatusBadRequest || decodeEnvelope(t, rec).Error.Code != "bad_request" {
			t.Errorf("%s: %d %s", path[:12], rec.Code, rec.Body.String())
		}
	}
}
