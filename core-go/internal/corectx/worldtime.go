package corectx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// worldTick is the resolution of timestamptz. The cutoff is "strictly before T", so the run's trigger
// activity, which occurred at its newest trigger's occurred_at, is visible when T is that plus one tick.
const worldTick = time.Microsecond

// maxEvidenceScan bounds the claims read for one field when the cutoff re-adjudicates them in memory. A
// field with more claims is cut at this bound and the packet says so (truncated); nothing is dropped silently.
// A variable so a test can lower it.
var maxEvidenceScan = 2000

// worldCutoff is the world time a run reads at (ADR-0019): its newest trigger activity's occurred_at plus one
// tick, derived here from the run and never from the caller; trigger is that occurred_at. Every run has a
// trigger activity (a CHECK on agent_runs); one whose triggers resolve to no activity cannot be placed in
// world time, and is refused (ErrRunInactive) rather than served current data, which would be the leak.
//
// It fails closed (ErrAmbiguousWorldTime) when another activity of the account occurred at the same instant
// as the trigger: the cutoff would include it, and world time cannot say whether the run could see it.
func worldCutoff(ctx context.Context, db claimstore.DB, accountID string, triggerIDs []string) (cutoff, trigger time.Time, err error) {
	var newest sql.NullTime
	list := uuidList(triggerIDs)
	if err := db.QueryRowContext(ctx, `SELECT max(occurred_at) FROM activities WHERE id = ANY(string_to_array($1, ',')::uuid[])`,
		list).Scan(&newest); err != nil {
		return cutoff, trigger, fmt.Errorf("corectx: read trigger time: %w", err)
	}
	if !newest.Valid {
		return cutoff, trigger, ErrRunInactive
	}
	var tie bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM activities WHERE account_id = $1::uuid AND occurred_at = $2
 AND NOT (id = ANY(string_to_array($3, ',')::uuid[])))`, accountID, newest.Time, list).Scan(&tie); err != nil {
		return cutoff, trigger, fmt.Errorf("corectx: check trigger ties: %w", err)
	}
	if tie {
		return cutoff, trigger, ErrAmbiguousWorldTime
	}
	trigger = newest.Time.UTC()
	return trigger.Add(worldTick), trigger, nil
}

// pinnedVersion is the state version the run was built on, or 0 when none is recorded.
func pinnedVersion(sc runScope) int {
	if !sc.stateVersion.Valid {
		return 0
	}
	return int(sc.stateVersion.Int32)
}

// loadState reads the account's state for the run: the version the run was pinned to (agent_runs.state_version),
// which must fold nothing at or after the cutoff (ErrStateAfterCutoff otherwise; a later version that shares
// its as_of can neither win nor hide it). A run with no recorded version reads the newest version strictly
// before the cutoff. ok is false while none has been computed.
func loadState(ctx context.Context, db claimstore.DB, sc runScope) (st reducer.AccountState, ok bool, err error) {
	version := pinnedVersion(sc)
	if version == 0 {
		return coalesce.StateBefore(ctx, db, sc.accountID, sc.cutoff)
	}
	raw, asOf, found, err := coalesce.StateVersion(ctx, db, sc.accountID, version)
	if err != nil || !found {
		return st, false, err
	}
	if !asOf.Before(sc.cutoff) {
		return st, false, fmt.Errorf("version %d has as_of %s, cutoff %s: %w", version, asOf.Format(time.RFC3339Nano), sc.cutoff.Format(time.RFC3339Nano), ErrStateAfterCutoff)
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, false, fmt.Errorf("corectx: decode state: %w", err)
	}
	return st, true, nil
}

// evidenceAsOf is the evidence tool at a cutoff: the claims of the field made before it, each with the status
// the claim had then (claimstore.ClaimsAsOf re-adjudicates), in the live tool's order: standing winners first.
func evidenceAsOf(ctx context.Context, db claimstore.DB, sc runScope, p Params) ([]any, bool, error) {
	standing, err := claimstore.ClaimsAsOf(ctx, db, sc.accountID, sc.cutoff)
	if err != nil {
		return nil, false, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id::text, source_activity_id::text, field_path, value, standing,
 confidence::float8, evidence_quote, subject_person_id::text, speaker_person_id::text, occurred_at, extractor
 FROM claims WHERE account_id = $1::uuid AND field_path = $2 AND occurred_at < $4
   AND source_activity_id IN (SELECT id FROM activities WHERE account_id = $1::uuid
                              AND permissions ->> 'visibility' = 'org' AND occurred_at < $4)
 ORDER BY standing_rank DESC, occurred_at DESC, confidence DESC, id LIMIT $3`,
		sc.accountID, p.FieldPath, maxEvidenceScan+1, sc.cutoff.UTC())
	if err != nil {
		return nil, false, fmt.Errorf("corectx: read claims: %w", err)
	}
	defer rows.Close()
	var all []claimItem
	scanned, scanCut := 0, false
	for rows.Next() {
		if scanned++; scanned > maxEvidenceScan {
			scanCut = true // more claims than the bound: say so, never drop silently
			break
		}
		var c claimItem
		var value []byte
		if err := rows.Scan(&c.ClaimID, &c.ActivityID, &c.FieldPath, &value, &c.Standing, &c.Confidence,
			&c.Quote, &c.SubjectPersonID, &c.SpeakerPersonID, &c.OccurredAt, &c.Extractor); err != nil {
			return nil, false, fmt.Errorf("corectx: scan claim: %w", err)
		}
		at, existed := standing[c.ClaimID]
		if !existed { // rejected by a human: no world time, so in no as-of answer
			continue
		}
		c.Value, c.OccurredAt, c.Status = value, c.OccurredAt.UTC(), string(at.Status)
		all = append(all, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	sort.SliceStable(all, func(i, j int) bool { // the SQL order, with the claims standing at the cutoff first
		return (all[i].Status == string(claims.StatusActive)) && (all[j].Status != string(claims.StatusActive))
	})
	items := make([]any, len(all))
	for i, c := range all {
		items[i] = c
	}
	page, more, err := cut(items, p.Limit)
	return page, more || scanCut, err
}
