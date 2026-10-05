package api

import (
	"errors"
	"net/http"

	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
)

// BreakerService is the provider circuit breaker the operator endpoints read and reset
// (*providerbreaker.Breaker implements it; HAR-135).
type BreakerService interface {
	Status() providerbreaker.Status
	Reset()
}

// WithBreaker serves GET /provider-breaker and POST /provider-breaker/reset.
func WithBreaker(b BreakerService) Option {
	return func(s *server) error {
		if b == nil {
			return errors.New("api: breaker service is required")
		}
		s.breaker = b
		return nil
	}
}

func (s *server) routeBreaker(mux *http.ServeMux) {
	if s.breaker == nil {
		return
	}
	mux.HandleFunc("GET /provider-breaker", s.operator(s.getBreaker))
	mux.HandleFunc("/provider-breaker", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("POST /provider-breaker/reset", s.operator(s.resetBreaker))
	mux.HandleFunc("/provider-breaker/reset", methodNotAllowed(http.MethodPost))
}

func (s *server) getBreaker(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.breaker.Status())
}

func (s *server) resetBreaker(w http.ResponseWriter, _ *http.Request) {
	s.breaker.Reset()
	writeJSON(w, http.StatusOK, s.breaker.Status())
}
