package knowledgestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// ErrDuplicateEvidence is returned when the same evidence is attached to the same knowledge twice.
var ErrDuplicateEvidence = errors.New("evidence already recorded for this knowledge")

// evidenceRef names the evidence behind a status change (knowledge_status_history.evidence_*).
type evidenceRef struct{ kind, id string }

// RecordEvidence attaches one piece of evidence, updates the counts and applies the lifecycle rules,
// all in the caller's transaction (the history trigger reads transaction-local settings). It returns
// the stored object and the status change, if any.
func RecordEvidence(ctx context.Context, tx *sql.Tx, id string, ev knowledge.Evidence, rules knowledge.Rules) (knowledge.Knowledge, *knowledge.Change, error) {
	k, err := get(ctx, tx, id, true)
	if err != nil {
		return knowledge.Knowledge{}, nil, err
	}
	next, change, err := knowledge.Record(k, ev, rules)
	if err != nil {
		return knowledge.Knowledge{}, nil, err
	}
	if err := insertEvidence(ctx, tx, id, ev); err != nil {
		return knowledge.Knowledge{}, nil, err
	}
	if err := save(ctx, tx, next, change, &evidenceRef{kind: ev.Kind, id: ev.RefID}); err != nil {
		return knowledge.Knowledge{}, nil, err
	}
	stored, err := Get(ctx, tx, id)
	return stored, change, err
}

// Revalidate applies the lifecycle rules at now without new evidence (the stale sweep).
func Revalidate(ctx context.Context, tx *sql.Tx, id string, now time.Time, rules knowledge.Rules) (knowledge.Knowledge, *knowledge.Change, error) {
	k, err := get(ctx, tx, id, true)
	if err != nil {
		return knowledge.Knowledge{}, nil, err
	}
	next, change := knowledge.EvaluateLifecycle(k, now, rules)
	if change == nil {
		return k, nil, nil
	}
	if err := save(ctx, tx, next, change, nil); err != nil {
		return knowledge.Knowledge{}, nil, err
	}
	stored, err := Get(ctx, tx, id)
	return stored, change, err
}

func insertEvidence(ctx context.Context, tx *sql.Tx, id string, ev knowledge.Evidence) error {
	note := sql.NullString{String: ev.Note, Valid: ev.Note != ""}
	if ev.Kind == knowledge.EvidenceCustomerReaction {
		note = sql.NullString{String: "polarity=" + ev.Polarity, Valid: true}
	}
	if ev.Kind == knowledge.EvidenceBusinessOutcome {
		note = sql.NullString{String: "outcome=" + ev.OutcomeType, Valid: true}
	}
	// SAVEPOINT so a duplicate does not abort the caller's transaction.
	if _, err := tx.ExecContext(ctx, `SAVEPOINT knowledge_evidence`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO knowledge_evidence (knowledge_id, kind, ref_id, note, created_at)
		VALUES ($1, $2, $3, $4, $5)`, id, ev.Kind, ev.RefID, note, ev.At.UTC())
	if err != nil {
		if _, rbErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT knowledge_evidence`); rbErr != nil {
			return fmt.Errorf("record evidence: %v (rollback: %w)", err, rbErr)
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "knowledge_evidence_once" {
			return fmt.Errorf("%w: %s %s", ErrDuplicateEvidence, ev.Kind, ev.RefID)
		}
		return fmt.Errorf("record evidence: %w", err)
	}
	_, err = tx.ExecContext(ctx, `RELEASE SAVEPOINT knowledge_evidence`)
	return err
}

// save writes the tallies and status; a status change carries its reason and evidence into history.
func save(ctx context.Context, tx *sql.Tx, k knowledge.Knowledge, change *knowledge.Change, ref *evidenceRef) error {
	reason := ""
	if change != nil {
		reason = change.Reason
	}
	if err := setChangeContext(ctx, tx, reason, ref); err != nil {
		return err
	}
	cols, err := encode(k)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE knowledge SET counts = $2, support_count = $3, counterexamples = $4,
		last_validated_at = $5, status = $6 WHERE id = $1`,
		k.ID, cols.counts, k.Counts.Decisions, cols.counterexamples, k.LastValidatedAt, k.Status)
	if err != nil {
		return fmt.Errorf("update knowledge %s: %w", k.ID, err)
	}
	return nil
}

// setChangeContext sets the transaction-local settings the status-history trigger reads (migration 0014).
// Every call sets all three, so a later change in the same transaction never inherits stale evidence.
func setChangeContext(ctx context.Context, tx *sql.Tx, reason string, ref *evidenceRef) error {
	kind, id := "", ""
	if ref != nil {
		kind, id = ref.kind, ref.id
	}
	_, err := tx.ExecContext(ctx, `SELECT set_config('ghost.knowledge_reason', $1, true),
		set_config('ghost.knowledge_evidence_kind', $2, true), set_config('ghost.knowledge_evidence_ref', $3, true)`,
		reason, kind, id)
	if err != nil {
		return fmt.Errorf("set knowledge change context: %w", err)
	}
	return nil
}
