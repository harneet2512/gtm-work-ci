// Package surfacemsg holds the create-only message refs that make a surface's channel message exactly-once
// (HAR-136, contracts/schemas/surface_message.v1.json). The outbox delivers at least once; a crash between a
// surface's post and its acknowledgement delivers an event again. The ref is the durable "this message already
// exists": a surface reserves the (subject, surface, kind) slot before posting, records the message's ts once
// after posting, and on a repeated delivery finds the ts and updates the message instead of posting a second.
// A reservation without a ts means a post may or may not have happened; the surface reconciles that against its
// channel before it posts again. Core never talks to Slack: it only keeps the refs.
package surfacemsg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	// ErrInvalid is a malformed subject, surface, kind, channel or ts.
	ErrInvalid = errors.New("surfacemsg: invalid request")
	// ErrNotFound is a slot that was never reserved.
	ErrNotFound = errors.New("surfacemsg: no such message ref")
)

// TSConflictError is a RecordTS of a ts different from the one already recorded: the caller's message is a
// duplicate. Existing is the recorded ref.
type TSConflictError struct{ Existing Ref }

func (e *TSConflictError) Error() string { return "surfacemsg: a different ts is already recorded" }

// Kinds of message (surface_message.v1.json).
const (
	KindBI       = "bi"
	KindChooser  = "chooser"
	KindJudgment = "judgment"
)

const maxChannelLen = 64

var (
	uuidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	surfacePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	tsPattern      = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
)

// Ref is contracts/schemas/surface_message.v1.json. TS is nil until the post is recorded.
type Ref struct {
	SubjectID  string    `json:"subject_id"`
	Surface    string    `json:"surface"`
	Kind       string    `json:"kind"`
	Channel    string    `json:"channel"`
	TS         *string   `json:"ts"`
	ReservedAt time.Time `json:"reserved_at"`
}

// Store reads and writes the refs.
type Store struct{ db *sql.DB }

// New returns a Store over db.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("surfacemsg: a database is required")
	}
	return &Store{db: db}, nil
}

func validKey(subject, surface, kind string) error {
	switch {
	case !uuidPattern.MatchString(subject):
		return fmt.Errorf("%w: subject_id must be a lower-case uuid", ErrInvalid)
	case !surfacePattern.MatchString(surface):
		return fmt.Errorf("%w: surface must match %s", ErrInvalid, surfacePattern)
	case kind != KindBI && kind != KindChooser && kind != KindJudgment:
		return fmt.Errorf("%w: kind must be bi, chooser or judgment", ErrInvalid)
	}
	return nil
}

const columns = `subject_id::text, surface, kind, channel, ts, reserved_at`

func scan(row interface{ Scan(...any) error }) (Ref, error) {
	var r Ref
	var ts sql.NullString
	if err := row.Scan(&r.SubjectID, &r.Surface, &r.Kind, &r.Channel, &ts, &r.ReservedAt); err != nil {
		return Ref{}, err
	}
	if ts.Valid {
		v := ts.String
		r.TS = &v
	}
	r.ReservedAt = r.ReservedAt.UTC()
	return r, nil
}

// Get returns the slot, or ErrNotFound.
func (s *Store) Get(ctx context.Context, subject, surface, kind string) (Ref, error) {
	if err := validKey(subject, surface, kind); err != nil {
		return Ref{}, err
	}
	r, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM surface_messages
 WHERE subject_id = $1::uuid AND surface = $2 AND kind = $3`, subject, surface, kind))
	if errors.Is(err, sql.ErrNoRows) {
		return Ref{}, ErrNotFound
	}
	if err != nil {
		return Ref{}, fmt.Errorf("surfacemsg: read ref: %w", err)
	}
	return r, nil
}

// Reserve creates the slot if it does not exist (created true: the caller owns the post) and otherwise returns
// the existing row unchanged (created false), whatever channel the caller named.
func (s *Store) Reserve(ctx context.Context, subject, surface, kind, channel string) (Ref, bool, error) {
	if err := validKey(subject, surface, kind); err != nil {
		return Ref{}, false, err
	}
	if channel == "" || len(channel) > maxChannelLen {
		return Ref{}, false, fmt.Errorf("%w: channel must be 1..%d characters", ErrInvalid, maxChannelLen)
	}
	r, err := scan(s.db.QueryRowContext(ctx, `INSERT INTO surface_messages (subject_id, surface, kind, channel)
 VALUES ($1::uuid, $2, $3, $4) ON CONFLICT (subject_id, surface, kind) DO NOTHING RETURNING `+columns, subject, surface, kind, channel))
	if err == nil {
		return r, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Ref{}, false, fmt.Errorf("surfacemsg: reserve: %w", err)
	}
	r, err = s.Get(ctx, subject, surface, kind)
	if err != nil {
		return Ref{}, false, err
	}
	return r, false, nil
}

// RecordTS writes the ts of the posted message once. The same ts again returns the ref; a different one is a
// *TSConflictError; a slot nobody reserved is ErrNotFound.
func (s *Store) RecordTS(ctx context.Context, subject, surface, kind, ts string) (Ref, error) {
	if err := validKey(subject, surface, kind); err != nil {
		return Ref{}, err
	}
	if !tsPattern.MatchString(ts) {
		return Ref{}, fmt.Errorf("%w: ts must look like 1759587744.000200", ErrInvalid)
	}
	r, err := scan(s.db.QueryRowContext(ctx, `UPDATE surface_messages SET ts = $4
 WHERE subject_id = $1::uuid AND surface = $2 AND kind = $3 AND (ts IS NULL OR ts = $4) RETURNING `+columns, subject, surface, kind, ts))
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Ref{}, fmt.Errorf("surfacemsg: record ts: %w", err)
	}
	existing, err := s.Get(ctx, subject, surface, kind)
	if err != nil {
		return Ref{}, err
	}
	return Ref{}, &TSConflictError{Existing: existing}
}
