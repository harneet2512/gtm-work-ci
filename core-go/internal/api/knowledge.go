package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
)

// KnowledgeService is what the HAR-97 §18 knowledge endpoints need; *knowledgestore.Service
// implements it. Reads return the stored object as the JSON knowledge.v1.json describes.
type KnowledgeService interface {
	List(ctx context.Context, status string, limit int) ([]byte, error)
	Doc(ctx context.Context, id string) ([]byte, error)
}

// WithKnowledge serves GET /knowledge and GET /knowledge/{knowledge_id} (WP24, HAR-122).
func WithKnowledge(svc KnowledgeService) Option {
	return func(s *server) error {
		if svc == nil {
			return errors.New("api: knowledge service is required")
		}
		s.knowledge = svc
		return nil
	}
}

// knowledgeStatuses is the lifecycle enum of knowledge.v1.json#properties/status; the filter
// accepts exactly these.
var knowledgeStatuses = map[string]bool{
	knowledge.StatusCandidate:   true,
	knowledge.StatusProvisional: true,
	knowledge.StatusSupported:   true,
	knowledge.StatusConfirmed:   true,
	knowledge.StatusDisputed:    true,
	knowledge.StatusStale:       true,
}

func (s *server) routeKnowledge(mux *http.ServeMux) {
	if s.knowledge == nil {
		return
	}
	mux.HandleFunc("GET /knowledge", s.operator(s.listKnowledge))
	mux.HandleFunc("/knowledge", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("GET /knowledge/{knowledge_id}", s.operator(s.getKnowledge))
	mux.HandleFunc("/knowledge/{knowledge_id}", methodNotAllowed(http.MethodGet))
}

func (s *server) listKnowledge(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r.URL.Query())
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !knowledgeStatuses[status] {
		writeError(w, http.StatusBadRequest, codeBadRequest, "status must be one of candidate, provisional, supported, confirmed, disputed, stale", nil)
		return
	}
	doc, err := s.knowledge.List(r.Context(), status, limit)
	s.knowledgeReply(w, r, doc, err)
}

func (s *server) getKnowledge(w http.ResponseWriter, r *http.Request) {
	doc, err := s.knowledge.Doc(r.Context(), r.PathValue("knowledge_id"))
	s.knowledgeReply(w, r, doc, err)
}

// knowledgeReply writes the stored document and maps ErrNotFound to the envelope; internal
// detail stays in the server log.
func (s *server) knowledgeReply(w http.ResponseWriter, r *http.Request, doc []byte, err error) {
	switch {
	case err == nil:
		writeRaw(w, http.StatusOK, doc)
	case errors.Is(err, knowledgestore.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "not found", nil)
	default:
		s.log.ErrorContext(r.Context(), "knowledge read failed", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}
