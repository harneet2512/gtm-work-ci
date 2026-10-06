// Package askstore is the Postgres ask.Store: the conversations, paused tasks and traces of Ask Cliff, kept by core
// so no Slack handler holds any of it (migration 0038).
package askstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/ask"
)

// Store implements ask.Store over a database.
type Store struct{ db *sql.DB }

// New returns a Store over db.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("askstore: a database is required")
	}
	return &Store{db: db}, nil
}

var _ ask.Store = (*Store)(nil)

// Turns returns the latest limit turns of a conversation, oldest first.
func (s *Store) Turns(ctx context.Context, ref string, limit int) ([]ask.Turn, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT role, body, created_at FROM (
			SELECT id, role, body, created_at FROM ask_turns WHERE thread_ref = $1 ORDER BY id DESC LIMIT $2
		) latest ORDER BY id ASC`, ref, max(limit, 1))
	if err != nil {
		return nil, fmt.Errorf("askstore: turns: %w", err)
	}
	defer rows.Close()
	var out []ask.Turn
	for rows.Next() {
		var t ask.Turn
		if err := rows.Scan(&t.Role, &t.Text, &t.At); err != nil {
			return nil, fmt.Errorf("askstore: turns: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AppendTurns stores turns in order, in one transaction.
func (s *Store) AppendTurns(ctx context.Context, ref string, turns ...ask.Turn) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("askstore: append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, t := range turns {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ask_turns (thread_ref, role, body, created_at) VALUES ($1, $2, $3, $4)`,
			ref, t.Role, t.Text, t.At); err != nil {
			return fmt.Errorf("askstore: append: %w", err)
		}
	}
	return tx.Commit()
}

// SetPending records the task paused on an action in a conversation; nil clears it.
func (s *Store) SetPending(ctx context.Context, ref string, p *ask.Pending) error {
	if p == nil {
		_, err := s.db.ExecContext(ctx, `DELETE FROM ask_pending WHERE thread_ref = $1`, ref)
		return wrap("clear pending", err)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ask_pending (thread_ref, kind, question, channel_kind, created_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (thread_ref) DO UPDATE SET kind = EXCLUDED.kind, question = EXCLUDED.question,
			channel_kind = EXCLUDED.channel_kind, created_at = EXCLUDED.created_at`,
		ref, p.Kind, p.Question, p.ChannelKind, p.At)
	return wrap("set pending", err)
}

// Pending returns the paused task of a conversation, or nil.
func (s *Store) Pending(ctx context.Context, ref string) (*ask.Pending, error) {
	var p ask.Pending
	err := s.db.QueryRowContext(ctx, `SELECT kind, question, channel_kind, created_at FROM ask_pending WHERE thread_ref = $1`, ref).
		Scan(&p.Kind, &p.Question, &p.ChannelKind, &p.At)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("askstore: pending: %w", err)
	}
	return &p, nil
}

// SaveTrace stores the trace of an answer.
func (s *Store) SaveTrace(ctx context.Context, t ask.Trace) error {
	body, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("askstore: trace: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO ask_traces (id, thread_ref, body, created_at) VALUES ($1, $2, $3::jsonb, $4)
		ON CONFLICT (id) DO UPDATE SET body = EXCLUDED.body`, t.ID, t.ThreadRef, string(body), t.CreatedAt)
	return wrap("save trace", err)
}

// Trace returns one trace; ask.ErrNotFound when there is none.
func (s *Store) Trace(ctx context.Context, id string) (ask.Trace, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM ask_traces WHERE id = $1`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return ask.Trace{}, ask.ErrNotFound
	}
	if err != nil {
		return ask.Trace{}, fmt.Errorf("askstore: trace: %w", err)
	}
	var t ask.Trace
	if err := json.Unmarshal(body, &t); err != nil {
		return ask.Trace{}, fmt.Errorf("askstore: trace: %w", err)
	}
	return t, nil
}

func wrap(what string, err error) error {
	if err != nil {
		return fmt.Errorf("askstore: %s: %w", what, err)
	}
	return nil
}
