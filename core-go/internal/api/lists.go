package api

import (
	"net/http"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// maxCursorLen bounds the opaque ?cursor= (core.yaml components.parameters.Cursor).
const maxCursorLen = 512

// parseCursor reads ?cursor=; it writes the 400 when the cursor is absurdly long (a cursor the core issued never is).
func parseCursor(w http.ResponseWriter, r *http.Request) (string, bool) {
	c := r.URL.Query().Get("cursor")
	if len(c) > maxCursorLen {
		writeError(w, http.StatusBadRequest, codeBadRequest, "cursor is too long", nil)
		return "", false
	}
	return c, true
}

// listAccounts serves GET /accounts (HAR-145): AccountSummary items ordered by name, keyset-paginated.
func (s *server) listAccounts(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r.URL.Query())
	if !ok {
		return
	}
	cursor, ok := parseCursor(w, r)
	if !ok {
		return
	}
	page, err := s.reads.ListAccounts(r.Context(), limit, cursor)
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// listRuns serves GET /runs (HAR-145): AgentRun items newest first, filtered by account and status.
func (s *server) listRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, ok := parseLimit(w, q)
	if !ok {
		return
	}
	cursor, ok := parseCursor(w, r)
	if !ok {
		return
	}
	page, err := s.reads.ListRuns(r.Context(), readmodel.RunFilter{
		AccountID: q.Get("account_id"), Status: q.Get("status"), Limit: limit, Cursor: cursor,
	})
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
