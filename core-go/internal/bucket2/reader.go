package bucket2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// Errors a caller maps to HTTP statuses.
var (
	// ErrNotFound: the episode does not exist.
	ErrNotFound = errors.New("bucket2: episode not found")
	// ErrInvalid: the episode id or the gate filter is malformed.
	ErrInvalid = errors.New("bucket2: invalid request")
)

// GatePattern matches every gate the shared store holds: Bucket 1 B1 to B9, Bucket 2 D1 to D10 and Bucket 3 S1 to S5.
// The store and the read route are generic across them (HAR-97), keyed by episode and gate: one table, one route.
var GatePattern = regexp.MustCompile(`^(B[1-9]|D([1-9]|10)|S[1-5])$`)

var episodeID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Reader serves the stored gate results of an episode as the gate_result.v1.json documents.
type Reader struct{ db *sql.DB }

// NewReader builds the reader.
func NewReader(db *sql.DB) (*Reader, error) {
	if db == nil {
		return nil, errors.New("bucket2: a database is required")
	}
	return &Reader{db: db}, nil
}

type resultDoc struct {
	ID           string   `json:"id"`
	Gate         string   `json:"gate"`
	SubGate      string   `json:"sub_gate"`
	Label        string   `json:"label"`
	JudgedObject judged   `json:"judged_object"`
	SpanID       string   `json:"span_id"`
	Verdict      Verdict  `json:"verdict"`
	Question     string   `json:"question"`
	Observed     string   `json:"observed"`
	Why          string   `json:"why"`
	EvidenceRefs []string `json:"evidence_refs"`
	Improves     string   `json:"improves"`
	Grader       Grader   `json:"grader"`
	Calibrated   bool     `json:"calibrated"`
}

type judged struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// GateResults returns {episode_id, items} for the episode in flow order, optionally narrowed to one gate (gate "" is
// all). An episode with no stored result answers an empty items list, never a 404: "not measured" is not an error.
func (r *Reader) GateResults(ctx context.Context, id, gate string) ([]byte, error) {
	if !episodeID.MatchString(id) || (gate != "" && !GatePattern.MatchString(gate)) {
		return nil, ErrInvalid
	}
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM decision_episodes WHERE id = $1::uuid)`, id).Scan(&exists); err != nil {
		return nil, fmt.Errorf("bucket2: check episode %s: %w", id, err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	stored, err := Load(ctx, r.db, id)
	if err != nil {
		return nil, err
	}
	items := make([]resultDoc, 0, len(stored))
	for _, s := range stored {
		if gate != "" && s.Gate != gate {
			continue
		}
		refs := s.EvidenceRefs
		if refs == nil {
			refs = []string{}
		}
		items = append(items, resultDoc{ID: s.ID, Gate: s.Gate, SubGate: s.SubGate, Label: s.Label, JudgedObject: judged{Type: s.JudgedType, ID: s.JudgedID},
			SpanID: s.SpanID, Verdict: s.Verdict, Question: s.Question, Observed: s.Observed, Why: s.Why, EvidenceRefs: refs,
			Improves: s.Improves, Grader: s.Grader, Calibrated: s.Calibrated})
	}
	return json.Marshal(map[string]any{"episode_id": id, "items": items})
}
