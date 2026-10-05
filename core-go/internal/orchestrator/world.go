package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
	"github.com/harneet2512/gtm-work/core-go/internal/transitionstore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// world is everything the run reads about the account at the replay clock, before any model is called.
type world struct {
	EventTime       time.Time // the replay clock: the trigger event's time, never the wall clock (invariant I3)
	State           reducer.AccountState
	Situation       knowledge.Situation
	Transition      *transitions.Record // open CANDIDATE/UNRESOLVED, else the newest CONFIRMED; nil when none
	Trigger         workerclient.TriggerContext
	StateDiffID     string
	AccountChangeID string
	BIUpdateID      string
}

// readWorld reads the deal's state (ADR-0016), the account's StateTransition and the trigger context, all as of
// the trigger event's time. Nothing here depends on time.Now.
func (s *Service) readWorld(ctx context.Context, run runRow) (world, error) {
	var w world
	var at sql.NullTime
	if err := s.db.QueryRowContext(ctx, `SELECT max(occurred_at) FROM activities WHERE id = ANY($1::uuid[])`,
		signalstore.UUIDArray(run.TriggerIDs)).Scan(&at); err != nil {
		return w, transient("read world", fmt.Errorf("read trigger time: %w", err))
	}
	if !at.Valid {
		return w, permanent("read world", errors.New("none of the run's trigger activities exists, so there is no replay clock"))
	}
	w.EventTime = at.Time.UTC()
	state, found, err := coalesce.StateAt(ctx, s.db, run.AccountID, w.EventTime, coalesce.WorldAsOf)
	if err != nil {
		return w, transient("read world", err)
	}
	if !found {
		return w, permanent("read world", fmt.Errorf("account %s has no state at the replay clock %s", run.AccountID, w.EventTime.Format(time.RFC3339)))
	}
	w.State = state
	sit, _, err := signalstore.SituationAt(ctx, s.db, run.AccountID, w.EventTime, coalesce.WorldAsOf)
	if err != nil {
		return w, transient("read world", err)
	}
	w.Situation = sit
	if w.Transition, err = transitionAt(ctx, s.db, state); err != nil {
		return w, transient("read world", err)
	}
	return w, s.readTrigger(ctx, run, &w)
}

// transitionAt is the StateTransition the account was in at the AccountState version `st`: the open transition
// its summary names, else the confirmed one behind its relationship state. The full record is the newest history
// snapshot evaluated at or before that state version, so a transition that moved on later is not read early.
func transitionAt(ctx context.Context, db transitionstore.Querier, st reducer.AccountState) (*transitions.Record, error) {
	id := ""
	switch {
	case st.OpenTransition != nil:
		id = st.OpenTransition.TransitionID
	case st.RelationshipState != nil && st.RelationshipState.Value != "unknown" && st.RelationshipState.TransitionID != nil:
		id = *st.RelationshipState.TransitionID
	default:
		return nil, nil
	}
	history, err := transitionstore.History(ctx, db, id)
	if err != nil {
		return nil, err
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].StateVersion <= st.Version {
			rec := history[i]
			return &rec, nil
		}
	}
	return nil, nil
}

func (s *Service) readTrigger(ctx context.Context, run runRow, w *world) error {
	var reasons, signalIDs string
	var diff sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT array_to_string(reason_codes, ','), array_to_string(signal_ids, ','), state_diff_id::text
 FROM trigger_evaluations WHERE id = $1::uuid`, run.EvaluationID).Scan(&reasons, &signalIDs, &diff); err != nil {
		return transient("read world", fmt.Errorf("read trigger evaluation: %w", err))
	}
	w.StateDiffID = diff.String
	w.Trigger = workerclient.TriggerContext{TriggerActivityIDs: run.TriggerIDs, ReasonCodes: splitList(reasons), SignalTypes: []string{}}
	if ids := splitList(signalIDs); len(ids) > 0 {
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT signal_type FROM signals WHERE id = ANY($1::uuid[]) ORDER BY 1`, signalstore.UUIDArray(ids))
		if err != nil {
			return transient("read world", fmt.Errorf("read signals: %w", err))
		}
		defer rows.Close()
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err != nil {
				return transient("read world", err)
			}
			w.Trigger.SignalTypes = append(w.Trigger.SignalTypes, t)
		}
		if err := rows.Err(); err != nil {
			return transient("read world", err)
		}
	}
	if run.OpportunityID != "" {
		var owner sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT owner_person_id::text FROM opportunities WHERE id = $1::uuid`, run.OpportunityID).Scan(&owner); err == nil && owner.Valid {
			w.Trigger.RepPersonID = &owner.String
		}
	}
	err := s.db.QueryRowContext(ctx, `SELECT c.id::text, COALESCE(b.id::text, '') FROM account_changes c
 LEFT JOIN business_intelligence_updates b ON b.account_change_id = c.id
 WHERE c.account_id = $1::uuid AND c.trigger_activity_ids && $2::uuid[] ORDER BY c.created_at DESC LIMIT 1`,
		run.AccountID, signalstore.UUIDArray(run.TriggerIDs)).Scan(&w.AccountChangeID, &w.BIUpdateID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return transient("read world", fmt.Errorf("read account change: %w", err))
	}
	return nil
}

func splitList(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

// header is the short state summary the worker starts from (everything else it pulls).
func (w world) header() string {
	f := w.State.Fields
	known := func(fld reducer.Field) string {
		if v, ok := fld.Value.(string); fld.Known && ok {
			return v
		}
		return "unknown"
	}
	parts := []string{fmt.Sprintf("%s (motion %s, stage %s, health %s)", orUnknown(w.State.AccountName),
		known(f.Motion), known(f.Stage), known(f.Health)), "champion status " + known(f.ChampionStatus)}
	if t := w.Transition; t != nil {
		to := "unresolved"
		if t.ToStateCandidate != nil {
			to = *t.ToStateCandidate
		}
		parts = append(parts, fmt.Sprintf("transition %s %s -> %s", t.Status, t.FromState, to))
	}
	text := strings.Join(parts, "; ")
	if r := []rune(text); len(r) > 1900 {
		text = string(r[:1900])
	}
	return text
}

func orUnknown(s string) string {
	if s == "" {
		return "account"
	}
	return s
}

// transitionFields are the status and suite-routing inputs of the account's transition ("" when none).
func (w world) transitionFields() (status, to, from string) {
	if w.Transition == nil {
		return "", "", ""
	}
	if w.Transition.ToStateCandidate != nil {
		to = *w.Transition.ToStateCandidate
	}
	return w.Transition.Status, to, w.Transition.FromState
}

// relationship is the account's confirmed relationship state ("" when unknown).
func (w world) relationship() string {
	if w.State.RelationshipState != nil && w.State.RelationshipState.Value != "unknown" {
		return w.State.RelationshipState.Value
	}
	return ""
}
