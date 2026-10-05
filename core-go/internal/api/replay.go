package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/play"
)

// PlayTimeout is the deadline of one POST /replay/play: the recompute and the graph projection of the event
// run inside it, so it is longer than a read but shorter than the server's write timeout (60 s). A Play that
// does not finish answers 504 and resumes when called again.
const PlayTimeout = 55 * time.Second

// ReplayService is what the Play endpoints need; *play.Service implements it.
type ReplayService interface {
	// Play releases the manifest's held-out event and returns the PlayResult document.
	Play(ctx context.Context, req play.Request) ([]byte, error)
	// Invisibility runs the event-N-invisible assertion.
	Invisibility(ctx context.Context, manifestID string) (play.Report, error)
	// Episodes is the episode replay view at the released cursor, or at `at` when not nil (HAR-129 §B/§C).
	Episodes(ctx context.Context, manifestID string, at *int) ([]byte, error)
	// NextEpisode releases exactly the next unreleased event and returns its bookkeeping.
	NextEpisode(ctx context.Context, manifestID string) ([]byte, error)
	// Reset moves the released cursor back to an already-released episode.
	Reset(ctx context.Context, manifestID string, episode int) ([]byte, error)
}

// WithReplay serves POST /replay/play, GET /replay/manifests/{manifest_id}/invisibility and the episode
// surface: GET /replay/manifests/{manifest_id}/episodes, POST .../episodes/next and POST .../reset.
func WithReplay(svc ReplayService) Option {
	return func(s *server) error {
		if svc == nil {
			return errors.New("api: replay service is required")
		}
		s.replay = svc
		return nil
	}
}

func (s *server) routeReplay(mux *http.ServeMux) {
	if s.replay == nil {
		return
	}
	mux.HandleFunc("POST /replay/play", s.operatorWithin(PlayTimeout, s.postPlay))
	mux.HandleFunc("/replay/play", methodNotAllowed(http.MethodPost))
	mux.HandleFunc("GET /replay/manifests/{manifest_id}/invisibility", s.operator(s.getInvisibility))
	mux.HandleFunc("/replay/manifests/{manifest_id}/invisibility", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("GET /replay/manifests/{manifest_id}/episodes", s.operator(s.getEpisodes))
	mux.HandleFunc("/replay/manifests/{manifest_id}/episodes", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("POST /replay/manifests/{manifest_id}/episodes/next", s.operatorWithin(PlayTimeout, s.postNextEpisode))
	mux.HandleFunc("/replay/manifests/{manifest_id}/episodes/next", methodNotAllowed(http.MethodPost))
	mux.HandleFunc("POST /replay/manifests/{manifest_id}/reset", s.operator(s.postReset))
	mux.HandleFunc("/replay/manifests/{manifest_id}/reset", methodNotAllowed(http.MethodPost))
}

func (s *server) postPlay(w http.ResponseWriter, r *http.Request) {
	var req play.Request
	if !decodeBody(w, r, &req, "play request") {
		return
	}
	if !play.IsUUID(req.ManifestID) || (req.EventID != "" && !play.IsUUID(req.EventID)) {
		writeError(w, http.StatusBadRequest, codeBadRequest, "manifest_id and event_id must be UUIDs", nil)
		return
	}
	doc, err := s.replay.Play(r.Context(), req)
	if err != nil {
		s.replayFailed(w, r, err)
		return
	}
	writeRawJSON(w, http.StatusOK, doc)
}

func (s *server) getInvisibility(w http.ResponseWriter, r *http.Request) {
	rep, err := s.replay.Invisibility(r.Context(), r.PathValue("manifest_id"))
	if err != nil {
		s.replayFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// getEpisodes is GET /replay/manifests/{manifest_id}/episodes: the view at the released cursor, or at the
// `at` query parameter (Previous). The document is the EpisodeReplayView JSON the service marshalled.
func (s *server) getEpisodes(w http.ResponseWriter, r *http.Request) {
	var at *int
	if raw := r.URL.Query().Get("at"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, codeBadRequest, "at must be a non-negative integer", nil)
			return
		}
		at = &n
	}
	doc, err := s.replay.Episodes(r.Context(), r.PathValue("manifest_id"), at)
	if err != nil {
		s.replayFailed(w, r, err)
		return
	}
	writeRawJSON(w, http.StatusOK, doc)
}

// postNextEpisode is POST /replay/manifests/{manifest_id}/episodes/next: release exactly one event. The
// held-out episode runs the whole Play inside the handler's deadline, like POST /replay/play.
func (s *server) postNextEpisode(w http.ResponseWriter, r *http.Request) {
	doc, err := s.replay.NextEpisode(r.Context(), r.PathValue("manifest_id"))
	if err != nil {
		s.replayFailed(w, r, err)
		return
	}
	writeRawJSON(w, http.StatusOK, doc)
}

// postReset is POST /replay/manifests/{manifest_id}/reset: move the released cursor back to an
// already-released episode.
func (s *server) postReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Episode *int `json:"episode"`
	}
	if !decodeBody(w, r, &req, "reset request") {
		return
	}
	if req.Episode == nil || *req.Episode < 0 {
		writeError(w, http.StatusBadRequest, codeBadRequest, "episode must be a non-negative integer", nil)
		return
	}
	doc, err := s.replay.Reset(r.Context(), r.PathValue("manifest_id"), *req.Episode)
	if err != nil {
		s.replayFailed(w, r, err)
		return
	}
	writeRawJSON(w, http.StatusOK, doc)
}

// writeRawJSON answers a JSON document the service already marshalled.
func writeRawJSON(w http.ResponseWriter, status int, doc []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(bytes.TrimSpace(doc), '\n'))
}

// replayFailed maps a Play error to the envelope. Internal detail stays in the server log; the 422 for a visible
// event names what leaked, because the operator must act on it.
func (s *server) replayFailed(w http.ResponseWriter, r *http.Request, err error) {
	var visible *play.VisibleError
	var released *play.AlreadyReleasedError
	switch {
	case errors.Is(err, play.ErrManifestNotFound):
		writeError(w, http.StatusNotFound, "manifest_not_found", "no such demo manifest", nil)
	case errors.Is(err, play.ErrReplayComplete):
		writeError(w, http.StatusConflict, "replay_complete", "every event of the manifest's sequence is already released", nil)
	case errors.Is(err, play.ErrInvalidEpisode):
		writeError(w, http.StatusUnprocessableEntity, "invalid_episode", err.Error(), nil)
	case errors.Is(err, play.ErrEpisodeTrail):
		writeError(w, http.StatusUnprocessableEntity, "episode_not_materialized", err.Error(), nil)
	case errors.Is(err, play.ErrKnowledgeUnavailable):
		s.log.WarnContext(r.Context(), "play: knowledge lifecycle rules unavailable", "error", err)
		writeError(w, http.StatusServiceUnavailable, "knowledge_unavailable", "the knowledge lifecycle rules are not configured", nil)
	case errors.As(err, &released):
		writeError(w, http.StatusConflict, "already_released", "the held-out event was already released; nothing changed",
			map[string]any{"account_change_id": released.AccountChangeID})
	case errors.Is(err, play.ErrPlayInProgress):
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusConflict, "play_in_progress", "another Play of this manifest is running; try again", nil)
	case errors.Is(err, play.ErrWrongEvent):
		writeError(w, http.StatusUnprocessableEntity, "wrong_event", "event_id is not the manifest's held-out event", nil)
	case errors.As(err, &visible):
		writeError(w, http.StatusUnprocessableEntity, "held_out_event_visible",
			"the held-out event is already visible to the system; nothing was released", map[string]any{"report": visible.Report})
	case errors.Is(err, play.ErrReleaseMismatch):
		s.log.WarnContext(r.Context(), "play: release does not match the manifest", "error", err)
		writeError(w, http.StatusUnprocessableEntity, "release_mismatch", "the replay event does not match the manifest or landed on another account", nil)
	case errors.Is(err, play.ErrGraphUnavailable), errors.Is(err, ctxgraph.ErrGraphUnavailable):
		s.log.WarnContext(r.Context(), "play: graph unavailable", "error", err, "path", r.URL.Path)
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "graph_unavailable", "the graph database is not available", nil)
	case errors.Is(err, play.ErrSourceUnavailable):
		s.log.WarnContext(r.Context(), "play: replay dataset unavailable", "error", err)
		writeError(w, http.StatusServiceUnavailable, "replay_source_unavailable", "the replay dataset cannot supply the held-out event", nil)
	case errors.Is(err, play.ErrTimeout), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		s.log.WarnContext(r.Context(), "play: did not finish", "error", err)
		writeError(w, http.StatusGatewayTimeout, "timeout", "the pipeline did not finish in time; call Play again to resume", nil)
	default:
		s.log.ErrorContext(r.Context(), "play failed", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}
