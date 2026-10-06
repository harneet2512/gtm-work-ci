package evaldispute

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// Service records and reads eval disputes; *Service implements api.EvalDisputeService.
type Service struct {
	db  *sql.DB
	log *slog.Logger
}

// New returns a Service over db. A nil logger discards logs.
func New(db *sql.DB, logger *slog.Logger) (*Service, error) {
	if db == nil {
		return nil, errors.New("evaldispute: database is required")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{db: db, log: logger}, nil
}

// Dispute is POST /eval-results/{eval_result_id}/disputes. It returns the EvalDispute JSON and whether it
// is new: an identical repeat (same result, surface, actor, reason and expected verdict) returns the stored
// dispute with created=false. The snapshot columns are copied from eval_runs in the insert itself, so they
// always describe the row that was disputed.
func (s *Service) Dispute(ctx context.Context, resultID string, req Request) ([]byte, bool, error) {
	if !IsUUID(resultID) {
		return nil, false, ErrNotFound
	}
	req, err := req.normalized()
	if err != nil {
		return nil, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("evaldispute: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := checkResult(ctx, tx, resultID, req.ExpectedVerdict); err != nil {
		return nil, false, err
	}
	if err := checkPerson(ctx, tx, req.ActorPersonID); err != nil {
		return nil, false, err
	}
	id, created, err := insert(ctx, tx, resultID, req)
	if err != nil {
		return nil, false, err
	}
	doc, err := read(ctx, tx, id)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("evaldispute: commit dispute of %s: %w", resultID, err)
	}
	s.log.InfoContext(ctx, "eval dispute recorded", "eval_result_id", resultID, "dispute_id", id, "created", created,
		"expected_verdict", deref(req.ExpectedVerdict), "surface", req.Surface)
	return doc, created, nil
}

// checkResult finds the disputed result and refuses an expected verdict equal to its own.
func checkResult(ctx context.Context, tx *sql.Tx, resultID string, expected *string) error {
	var verdict string
	err := tx.QueryRowContext(ctx, `SELECT verdict FROM eval_runs WHERE id = $1::uuid`, resultID).Scan(&verdict)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("evaldispute: read eval result %s: %w", resultID, err)
	}
	if expected != nil && sameVerdict(*expected, verdict) {
		return refuse(CodeExpectedEqualsVerdict, "the eval already says %s; to dispute only its reasoning, leave expected_verdict out", verdict)
	}
	return nil
}

func checkPerson(ctx context.Context, tx *sql.Tx, personID *string) error {
	if personID == nil {
		return nil
	}
	var known bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM people WHERE id = $1::uuid)`, *personID).Scan(&known); err != nil {
		return fmt.Errorf("evaldispute: read person %s: %w", *personID, err)
	}
	if !known {
		return refuse(CodeUnknownPerson, "actor_person_id names no known person")
	}
	return nil
}

const insertDispute = `
INSERT INTO eval_disputes (eval_result_id, agent_run_id, draft_index, eval_type, eval_version, disputed_verdict,
  disputed_blocking, expected_verdict, reason, surface, actor_person_id, actor_label)
SELECT id, agent_run_id, draft_index, evaluator, evaluator_version, verdict, blocking, $2, $3, $4, $5::uuid, $6
  FROM eval_runs WHERE id = $1::uuid
ON CONFLICT (eval_result_id, surface, actor_label, md5(reason), (coalesce(expected_verdict, ''))) DO NOTHING
RETURNING id::text`

const existingDispute = `
SELECT id::text FROM eval_disputes
 WHERE eval_result_id = $1::uuid AND surface = $2 AND actor_label = $3 AND reason = $4
   AND coalesce(expected_verdict, '') = coalesce($5, '')`

// insert writes the dispute, or finds the identical one already stored.
func insert(ctx context.Context, tx *sql.Tx, resultID string, req Request) (string, bool, error) {
	var id string
	err := tx.QueryRowContext(ctx, insertDispute, resultID, req.ExpectedVerdict, req.Reason, req.Surface, req.ActorPersonID, req.ActorLabel).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("evaldispute: insert dispute of %s: %w", resultID, err)
	}
	if err := tx.QueryRowContext(ctx, existingDispute, resultID, req.Surface, req.ActorLabel, req.Reason, req.ExpectedVerdict).Scan(&id); err != nil {
		return "", false, fmt.Errorf("evaldispute: read the identical dispute of %s: %w", resultID, err)
	}
	return id, false, nil
}

// dispute is eval_dispute.v1.json; the nullable properties are pointers so they encode as null.
type dispute struct {
	ID               string    `json:"id"`
	EvalResultID     string    `json:"eval_result_id"`
	AgentRunID       string    `json:"agent_run_id"`
	DraftIndex       int       `json:"draft_index"`
	EvalType         string    `json:"eval_type"`
	EvalVersion      string    `json:"eval_version"`
	DisputedVerdict  string    `json:"disputed_verdict"`
	DisputedBlocking bool      `json:"disputed_blocking"`
	ExpectedVerdict  *string   `json:"expected_verdict"`
	Reason           string    `json:"reason"`
	Surface          string    `json:"surface"`
	ActorPersonID    *string   `json:"actor_person_id"`
	ActorLabel       string    `json:"actor_label"`
	CreatedAt        time.Time `json:"created_at"`
}

func read(ctx context.Context, tx *sql.Tx, id string) ([]byte, error) {
	var d dispute
	var expected, person sql.NullString
	err := tx.QueryRowContext(ctx, `
SELECT id::text, eval_result_id::text, agent_run_id::text, draft_index, eval_type::text, eval_version, disputed_verdict,
       disputed_blocking, expected_verdict, reason, surface, actor_person_id::text, actor_label, created_at
  FROM eval_disputes WHERE id = $1::uuid`, id).Scan(&d.ID, &d.EvalResultID, &d.AgentRunID, &d.DraftIndex, &d.EvalType,
		&d.EvalVersion, &d.DisputedVerdict, &d.DisputedBlocking, &expected, &d.Reason, &d.Surface, &person, &d.ActorLabel, &d.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("evaldispute: read dispute %s: %w", id, err)
	}
	d.ExpectedVerdict, d.ActorPersonID = nullable(expected), nullable(person)
	d.CreatedAt = d.CreatedAt.UTC()
	doc, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("evaldispute: encode dispute %s: %w", id, err)
	}
	return doc, nil
}

func nullable(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// sameVerdict compares two verdicts as a reader does: unknown and its older spelling abstain are one verdict.
func sameVerdict(a, b string) bool {
	canonical := func(v string) string {
		if v == "abstain" {
			return "unknown"
		}
		return v
	}
	return canonical(a) == canonical(b)
}
