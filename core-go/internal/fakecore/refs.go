// The surface_messages of the contract in memory, served by fakecore under
// /surface-messages/{subject_id}/{surface}/{kind} so a Publisher over CoreHTTP is exactly-once against the
// fake core as it is in production (HAR-136). NewRefs also hands the routes to a test whose core stand-in
// is not fakecore; mount it on the /surface-messages prefix.
package fakecore

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var tsPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

type refRow struct {
	SubjectID  string    `json:"subject_id"`
	Surface    string    `json:"surface"`
	Kind       string    `json:"kind"`
	Channel    string    `json:"channel"`
	TS         *string   `json:"ts"`
	ReservedAt time.Time `json:"reserved_at"`
}

// Refs keeps the contract's refs: the reservation is atomic and idempotent, the ts is write-once and a
// different second ts is a 409 carrying the recorded one. ServeHTTP routes the three contract paths for a
// stand-alone mount; Server's route table calls getRef/reserveRef/recordRefTS directly.
type Refs struct {
	mu   sync.Mutex
	now  func() time.Time
	rows map[string]refRow
}

// NewRefs returns an empty store clocked by now (time.Now when nil).
func NewRefs(now func() time.Time) *Refs {
	if now == nil {
		now = time.Now
	}
	return &Refs{rows: map[string]refRow{}, now: now}
}

// ServeHTTP routes GET /surface-messages/{subject}/{surface}/{kind}, POST .../reservation and PUT .../ts;
// anything else is a 404.
func (rs *Refs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	a := fail(http.StatusNotFound, "not_found", "no such route")
	if len(p) >= 4 && p[0] == "surface-messages" {
		subject, surface, kind := p[1], p[2], p[3]
		switch {
		case len(p) == 4 && r.Method == http.MethodGet:
			a = rs.getRef(subject, surface, kind)
		case len(p) == 5 && p[4] == "reservation" && r.Method == http.MethodPost:
			var body struct {
				Channel string `json:"channel"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Channel == "" {
				a = fail(http.StatusBadRequest, "invalid_request", "a channel is required")
			} else {
				a = rs.reserveRef(subject, surface, kind, body.Channel)
			}
		case len(p) == 5 && p[4] == "ts" && r.Method == http.MethodPut:
			a = rs.tsFromBody(r, subject, surface, kind)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(a.status)
	raw, _ := json.Marshal(a.body)
	_, _ = w.Write(append(raw, '\n'))
}

// tsFromBody decodes and validates the PUT ts request for ServeHTTP (Server's spec check does it there).
func (rs *Refs) tsFromBody(r *http.Request, subject, surface, kind string) answer {
	var body struct {
		TS string `json:"ts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !tsPattern.MatchString(body.TS) {
		return fail(http.StatusBadRequest, "invalid_request", "a Slack-shaped ts is required")
	}
	return rs.recordRefTS(subject, surface, kind, body.TS)
}

func (rs *Refs) getRef(subject, surface, kind string) answer {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	row, found := rs.rows[refKey(subject, surface, kind)]
	if !found {
		return fail(http.StatusNotFound, "not_found", "the slot was never reserved")
	}
	return ok(row)
}

func (rs *Refs) reserveRef(subject, surface, kind, channel string) answer {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	k := refKey(subject, surface, kind)
	if row, found := rs.rows[k]; found {
		return ok(map[string]any{"created": false, "message": row})
	}
	row := refRow{SubjectID: subject, Surface: surface, Kind: kind, Channel: channel, ReservedAt: rs.now().UTC()}
	rs.rows[k] = row
	return ok(map[string]any{"created": true, "message": row})
}

func (rs *Refs) recordRefTS(subject, surface, kind, ts string) answer {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	k := refKey(subject, surface, kind)
	row, found := rs.rows[k]
	if !found {
		return fail(http.StatusNotFound, "not_found", "the slot was never reserved")
	}
	if row.TS != nil && *row.TS != ts {
		return fail(http.StatusConflict, "ts_conflict", "a different ts is already recorded")
	}
	row.TS = &ts
	rs.rows[k] = row
	return ok(row)
}

func refKey(subject, surface, kind string) string { return subject + "/" + surface + "/" + kind }

// getSurfaceMessage serves GET /surface-messages/{subject_id}/{surface}/{kind}.
func (s *Server) getSurfaceMessage(r *http.Request, _ []byte) answer {
	return s.refs.getRef(r.PathValue("subject_id"), r.PathValue("surface"), r.PathValue("kind"))
}

// reserveSurfaceMessage serves POST .../reservation; the spec check already validated {channel}.
func (s *Server) reserveSurfaceMessage(r *http.Request, body []byte) answer {
	var req struct {
		Channel string `json:"channel"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Channel == "" {
		return fail(http.StatusBadRequest, "invalid_request", "a channel is required")
	}
	return s.refs.reserveRef(r.PathValue("subject_id"), r.PathValue("surface"), r.PathValue("kind"), req.Channel)
}

// recordSurfaceMessageTS serves PUT .../ts (PUT bodies bypass the spec check, so the ts pattern is
// validated here).
func (s *Server) recordSurfaceMessageTS(r *http.Request, body []byte) answer {
	var req struct {
		TS string `json:"ts"`
	}
	if err := json.Unmarshal(body, &req); err != nil || !tsPattern.MatchString(req.TS) {
		return fail(http.StatusBadRequest, "invalid_request", "a Slack-shaped ts is required")
	}
	return s.refs.recordRefTS(r.PathValue("subject_id"), r.PathValue("surface"), r.PathValue("kind"), req.TS)
}
