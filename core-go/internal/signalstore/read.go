package signalstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/signalhistory"
)

// LoadRecords returns the account's persisted signals that may be open at t: occurred by t and not past
// their window at t. A zero t returns every signal of the account. Under coalesce.KnownAt a signal also had
// to be recorded by t (created_at <= t), so an event we learned about later does not leak into an earlier
// "what did we believe" view; under coalesce.WorldAsOf only its occurrence counts. The subject item key of
// a signal that reports a list item (a blocker, a commitment) is resolved from the claim behind it.
func LoadRecords(ctx context.Context, db claimstore.DB, accountID string, t time.Time, basis coalesce.Basis) ([]signalhistory.Record, error) {
	rows, err := db.QueryContext(ctx, `
SELECT s.id::text, s.signal_type, COALESCE(s.opportunity_id::text, ''), COALESCE(s.subject_claim_id::text, ''),
       s.details, s.evidence_refs, s.occurred_at, s.expires_at, c.field_path, c.value
  FROM signals s LEFT JOIN claims c ON c.id = s.subject_claim_id
 WHERE s.account_id = $1::uuid AND ($2::timestamptz IS NULL OR (s.occurred_at <= $2 AND (s.expires_at IS NULL OR s.expires_at >= $2)
       AND ($3::boolean IS NOT TRUE OR s.created_at <= $2)))
 ORDER BY s.occurred_at, s.id`, accountID, asParam(t), basis == coalesce.KnownAt)
	if err != nil {
		return nil, fmt.Errorf("signalstore: load signals of %s as of %v: %w", accountID, t, err)
	}
	defer rows.Close()
	var out []signalhistory.Record
	for rows.Next() {
		var r signalhistory.Record
		var details, refs, claimValue []byte
		var fieldPath *string
		var expires *time.Time
		if err := rows.Scan(&r.ID, &r.Type, &r.OpportunityID, &r.SubjectClaimID, &details, &refs, &r.OccurredAt, &expires, &fieldPath, &claimValue); err != nil {
			return nil, fmt.Errorf("signalstore: scan signal: %w", err)
		}
		r.OccurredAt = r.OccurredAt.UTC()
		if expires != nil {
			e := expires.UTC()
			r.ExpiresAt = &e
		}
		if err := json.Unmarshal(details, &r.Details); err != nil {
			return nil, fmt.Errorf("signalstore: decode details of signal %s: %w", r.ID, err)
		}
		if r.EvidenceRefs, err = decodeRefs(refs); err != nil {
			return nil, fmt.Errorf("signalstore: decode evidence of signal %s: %w", r.ID, err)
		}
		r.SubjectItemKey = itemKey(fieldPath, claimValue)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("signalstore: read signals: %w", err)
	}
	return out, nil
}

func asParam(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func decodeRefs(raw []byte) ([]knowledge.EvidenceRef, error) {
	var refs []knowledge.EvidenceRef
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil, err
	}
	return refs, nil
}

// itemKey is claims.ItemKey of the list item a claim asserts; "" for scalar fields or unparsable values.
func itemKey(fieldPath *string, value []byte) string {
	if fieldPath == nil || !claims.FieldPath(*fieldPath).IsList() {
		return ""
	}
	item, err := claims.ParseListItem(value)
	if err != nil {
		return ""
	}
	return claims.ItemKey(item.Text)
}

// OpenSignalsAsOf is "signals open as of T for account/opportunity": the persisted signals the matcher
// should see, oldest first, timed at their occurred_at (ADR-0015). Standing signals are returned while
// their window is open; the matcher still checks their condition against the state as of T.
func OpenSignalsAsOf(ctx context.Context, db claimstore.DB, accountID, opportunityID string, t time.Time, basis coalesce.Basis) ([]knowledge.Signal, error) {
	if t.IsZero() {
		return nil, errors.New("signalstore: as-of time is required")
	}
	recs, err := LoadRecords(ctx, db, accountID, t, basis)
	if err != nil {
		return nil, err
	}
	return signalhistory.OpenAsOf(recs, opportunityID, t), nil
}

// SituationAt builds the knowledge matcher's Situation for an account at t: the state as of t (basis says which
// clock, coalesce.KnownAt or coalesce.WorldAsOf) plus the signals open at t. With a primary deal the state is the
// deal's own OpportunityState (fields, buying group, gaps of one deal); without one (or before the deal had a
// state version) it is the AccountState. found is
// false when the account had no state yet. The relationship state and open transition (ADR-0012) come from the
// AccountState as of t.
func SituationAt(ctx context.Context, db claimstore.DB, accountID string, t time.Time, basis coalesce.Basis) (s knowledge.Situation, found bool, err error) {
	st, found, err := coalesce.StateAt(ctx, db, accountID, t, basis)
	if err != nil || !found {
		return knowledge.Situation{}, false, err
	}
	opp := ""
	if st.OpportunityID != nil {
		opp = *st.OpportunityID
	}
	open, err := OpenSignalsAsOf(ctx, db, accountID, opp, t, basis)
	if err != nil {
		return knowledge.Situation{}, false, err
	}
	// The account's buying group and gaps span its open deals; paired with the primary deal's headline fields they
	// would describe a mixture. When the account has a primary deal, match against that deal's own state (ADR-0016).
	if opp != "" {
		deal, ok, err := coalesce.OpportunityStateAt(ctx, db, accountID, opp, t, basis)
		if err != nil {
			return knowledge.Situation{}, false, err
		}
		if ok {
			rel, transition := knowledge.RelationshipOf(st) // relationship state is the account's, whichever deal is matched
			return knowledge.FromOpportunityState(deal, t, open, rel, transition), true, nil
		}
	}
	return knowledge.FromAccountState(st, t, open, "", nil), true, nil
}
