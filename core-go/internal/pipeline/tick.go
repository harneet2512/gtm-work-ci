package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/signals"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// TickResult counts what one clock tick looked at and wrote.
type TickResult struct {
	Accounts int
	Signals  int // signals the rules emitted (re-emitted ones included; the store writes each once)
}

// Tick evaluates the time-driven signal rules (the ghost.clock rules that need no activity) for every
// account at now and persists the signals. It is idempotent: ticking twice, or ticking after a
// ghost.clock activity already produced the same silence, writes nothing new (dedupe keys).
// Time signals alone never create a trigger evaluation: customer_went_silent is not a reason to run
// the follow-up workflow, and the next recompute folds any real activity into a diff as usual.
func Tick(ctx context.Context, db *sql.DB, now time.Time) (TickResult, error) {
	rows, err := db.QueryContext(ctx, `SELECT state FROM account_state ORDER BY account_id`)
	if err != nil {
		return TickResult{}, fmt.Errorf("pipeline: read account states: %w", err)
	}
	var states []reducer.AccountState
	for rows.Next() {
		var raw []byte
		var st reducer.AccountState
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return TickResult{}, fmt.Errorf("pipeline: scan account state: %w", err)
		}
		if err := json.Unmarshal(raw, &st); err != nil {
			_ = rows.Close()
			return TickResult{}, fmt.Errorf("pipeline: decode account state: %w", err)
		}
		states = append(states, st)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return TickResult{}, fmt.Errorf("pipeline: read account states: %w", err)
	}
	if err := rows.Close(); err != nil {
		return TickResult{}, fmt.Errorf("pipeline: read account states: %w", err)
	}
	res := TickResult{Accounts: len(states)}
	for _, st := range states {
		emitted := signals.Tick(st, now)
		res.Signals += len(emitted)
		scope := signalstore.Scope{AccountID: st.AccountID, OpportunityID: opportunity(st), CreatedAt: now}
		if _, err := signalstore.InsertSignals(ctx, db, scope, emitted); err != nil {
			return res, err
		}
	}
	return res, nil
}
