package ctxgraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// Errors the API maps to statuses.
var (
	// ErrEventUnknown: no such source event (404).
	ErrEventUnknown = errors.New("ctxgraph: event not found")
	// ErrGraphUnavailable: Neo4j could not be reached (503).
	ErrGraphUnavailable = errors.New("ctxgraph: graph database unavailable")
)

// Service is the read side the HTTP API serves: the operator neighborhood (not visibility-filtered, like
// the other operator reads), the per-event graph diff and the projection lag.
type Service struct {
	*Reader
	db *sql.DB
}

// NewService wraps a Reader.
func NewService(r *Reader, db *sql.DB) *Service { return &Service{Reader: r, db: db} }

// Neighborhood is the operator view of an account's graph.
func (s *Service) Neighborhood(ctx context.Context, accountID string, p Params) (View, error) {
	if !readmodel.ValidUUID(accountID) {
		return View{}, ErrAccountUnknown
	}
	v, err := s.Reader.Neighborhood(ctx, accountID, p, nil)
	return v, wrapNeo4j(err)
}

// EventDiff is the exact graph diff of a source event.
func (s *Service) EventDiff(ctx context.Context, eventID string) (EventDiff, error) {
	if !readmodel.ValidUUID(eventID) {
		return EventDiff{}, ErrEventUnknown
	}
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM source_events WHERE id = $1::uuid`, eventID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return EventDiff{}, ErrEventUnknown
	}
	if err != nil {
		return EventDiff{}, fmt.Errorf("ctxgraph: check event: %w", err)
	}
	return DiffForEvent(ctx, s.db, eventID)
}

// Lag is the projection lag metric as of now.
func (s *Service) Lag(ctx context.Context) (Lag, error) {
	return ReadLag(ctx, s.db, s.clk.Now())
}

// wrapNeo4j marks driver connectivity failures as ErrGraphUnavailable.
func wrapNeo4j(err error) error {
	if err != nil && neo4j.IsConnectivityError(err) {
		return fmt.Errorf("%w: %v", ErrGraphUnavailable, err)
	}
	return err
}
