package demorun

import (
	"context"
	"database/sql"
	"sync"

	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

// LiveCore is the PlayCore of the real demo: core over HTTP, and Postgres (opened on first use, only `play`
// needs it) for the list of the runs a Play opened.
type LiveCore struct {
	CoreClient
	DSN string

	once sync.Once
	db   *sql.DB
	err  error
}

// PlayRuns implements PlayCore.
func (l *LiveCore) PlayRuns(ctx context.Context, manifestID string) ([]RunInfo, error) {
	l.once.Do(func() { l.db, l.err = store.Open(ctx, l.DSN) })
	if l.err != nil {
		return nil, l.err
	}
	return DemoCore{CoreClient: l.CoreClient, DB: l.db}.PlayRuns(ctx, manifestID)
}

// Close releases the database if it was opened.
func (l *LiveCore) Close() {
	if l.db != nil {
		_ = l.db.Close()
	}
}

var _ PlayCore = (*LiveCore)(nil)
