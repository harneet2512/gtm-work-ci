package transitionstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Detector is the transition detector as the recompute transaction runs it: after the new
// AccountState version and its history row are written, it evaluates the rule set, writes the
// StateTransition the evidence earns (if any change) and exposes the account's confirmed
// relationship state and open transition on that same state version. There is no clock, model or
// network in it; same database rows, same transition.
type Detector struct {
	rules transitions.RuleSet
	paths []string // claim field paths the rule set reads
}

// NewDetector builds a detector over a validated rule set.
func NewDetector(rules transitions.RuleSet) Detector {
	return Detector{rules: rules, paths: rules.ClaimPaths()}
}

// Detect evaluates and persists. triggerActivityIDs are the activities whose recompute this is.
func (d Detector) Detect(ctx context.Context, tx *sql.Tx, st reducer.AccountState, deals []reducer.OpportunityState, triggerActivityIDs []string) (reducer.RelationshipState, *reducer.OpenTransition, error) {
	trigger := triggerActivityIDs
	if len(trigger) == 0 && st.LastActivityID != nil {
		trigger = []string{*st.LastActivityID}
	}
	if len(trigger) == 0 {
		return reducer.RelationshipState{}, nil, errors.New("transitionstore: a recompute without trigger activities cannot be recorded on a transition")
	}
	open, err := LoadOpen(ctx, tx, st.AccountID)
	if err != nil {
		return reducer.RelationshipState{}, nil, err
	}
	rel, err := Current(ctx, tx, st.AccountID)
	if err != nil {
		return reducer.RelationshipState{}, nil, err
	}
	in, err := d.input(ctx, tx, st, deals, rel, open)
	if err != nil {
		return reducer.RelationshipState{}, nil, err
	}
	meta := transitions.Meta{AccountID: st.AccountID, OpportunityID: st.OpportunityID, CurrentState: rel.State, StateVersion: st.Version,
		RuleSetVersion: d.rules.Version, AsOf: st.AsOf.UTC(), ComputedAt: st.ComputedAt.UTC(), TriggerActivityIDs: trigger}
	next, changed, err := transitions.Plan(open, transitions.Evaluate(d.rules, in), meta)
	if err != nil {
		return reducer.RelationshipState{}, nil, fmt.Errorf("transitionstore: account %s: %w", st.AccountID, err)
	}
	if changed {
		stored, err := Save(ctx, tx, *next)
		if err != nil {
			return reducer.RelationshipState{}, nil, err
		}
		open, rel = openAfter(stored), relAfter(stored, rel)
	}
	if err := patchState(ctx, tx, st, rel, open); err != nil {
		return reducer.RelationshipState{}, nil, err
	}
	return relationshipOf(rel), openSummary(open), nil
}

// openAfter is the open transition once stored has been written.
func openAfter(stored transitions.Record) *transitions.Record {
	if stored.Open() {
		return &stored
	}
	return nil
}

func relAfter(stored transitions.Record, before Relationship) Relationship {
	if stored.Status == transitions.StatusConfirmed && stored.ToStateCandidate != nil {
		return Relationship{State: *stored.ToStateCandidate, TransitionID: stored.ID, ConfirmedAt: stored.ConfirmedAt}
	}
	return before
}

// input assembles the evaluation input from the state, the account's claims and its open signals.
func (d Detector) input(ctx context.Context, q Querier, st reducer.AccountState, deals []reducer.OpportunityState, rel Relationship, open *transitions.Record) (transitions.Input, error) {
	var in transitions.Input
	raw, err := json.Marshal(st)
	if err != nil {
		return in, fmt.Errorf("transitionstore: encode state: %w", err)
	}
	if err := json.Unmarshal(raw, &in.State); err != nil {
		return in, fmt.Errorf("transitionstore: read state: %w", err)
	}
	in.State.RelationshipState = transitions.RelationshipState{Value: rel.State, ConfirmedAt: rel.ConfirmedAt}
	if in.Deals, err = dealsOf(deals); err != nil {
		return in, err
	}
	in.Now = st.AsOf.UTC()
	computed := st.ComputedAt.UTC()
	in.ComputedAt = &computed
	if open != nil {
		in.Open = open.AsOpen()
	}
	if in.Claims, err = loadClaims(ctx, q, st, d.rules, d.paths); err != nil {
		return in, err
	}
	if in.Signals, err = loadSignals(ctx, q, st); err != nil {
		return in, err
	}
	standing, err := claimStandings(ctx, q, st, d.rules, transitions.CitedClaimIDs(in.Signals))
	if err != nil {
		return in, err
	}
	in.Signals = transitions.MarkFirstParty(d.rules, in.Signals, standing)
	return in, nil
}

// dealsOf converts the per-deal states of the recompute into the rules' view of them (ADR-0016).
func dealsOf(states []reducer.OpportunityState) ([]transitions.Deal, error) {
	out := make([]transitions.Deal, 0, len(states))
	for _, s := range states {
		raw, err := json.Marshal(s)
		if err != nil {
			return nil, fmt.Errorf("transitionstore: encode deal %s: %w", s.OpportunityID, err)
		}
		var d transitions.Deal
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("transitionstore: read deal %s: %w", s.OpportunityID, err)
		}
		out = append(out, d)
	}
	return out, nil
}

// eligibleSQL is the SQL form of "this claim may earn a state": a status the rule set counts, not an AI suggestion
// below the adjudication confidence floor, not expired at the evaluated version's as_of. The arguments are the
// parameter numbers of the statuses (comma-joined), as_of and the confidence floor.
func eligibleSQL(statuses, asOf, floor int) string {
	return fmt.Sprintf(`string_to_array($%d, ',') @> ARRAY[c.status]
	AND NOT (c.standing = 'first_party_ai' AND c.confidence < $%d) AND (c.expires_at IS NULL OR c.expires_at > $%d)`, statuses, floor, asOf)
}

func loadClaims(ctx context.Context, q Querier, st reducer.AccountState, rules transitions.RuleSet, paths []string) ([]transitions.Claim, error) {
	accountID := st.AccountID
	rows, err := q.QueryContext(ctx, `SELECT c.id::text, c.field_path, c.status, c.standing, c.occurred_at, COALESCE(c.subject_person_id::text, ''),
		c.source_activity_id::text, COALESCE(c.opportunity_id::text, ''), COALESCE(c.value ->> 'stance', '') = 'withdrawn' FROM claims c WHERE c.account_id = $1::uuid AND `+eligibleSQL(2, 3, 4)+`
		AND c.field_path = ANY(string_to_array($5, ',')) ORDER BY c.occurred_at, c.id`,
		accountID, strings.Join(rules.ClaimStatuses, ","), st.AsOf.UTC(), claims.MinAIConfidence, strings.Join(paths, ","))
	if err != nil {
		return nil, fmt.Errorf("transitionstore: load claims of %s: %w", accountID, err)
	}
	defer rows.Close()
	var out []transitions.Claim
	for rows.Next() {
		var c transitions.Claim
		if err := rows.Scan(&c.ID, &c.FieldPath, &c.Status, &c.Standing, &c.OccurredAt, &c.SubjectPersonID, &c.SourceActivityID, &c.OpportunityID, &c.Withdrawn); err != nil {
			return nil, fmt.Errorf("transitionstore: scan claim: %w", err)
		}
		c.OccurredAt = c.OccurredAt.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// loadSignals reads the account's signals and decides openness with the knowledge matcher's rule (ADR-0011)
// against the evaluated state, so a STANDING signal is open only while its condition still holds.
func loadSignals(ctx context.Context, q Querier, st reducer.AccountState) ([]transitions.Signal, error) {
	rows, err := q.QueryContext(ctx, `SELECT id::text, signal_type, COALESCE(opportunity_id::text, ''), COALESCE(subject_person_id::text, ''),
		details, evidence_refs, occurred_at FROM signals WHERE account_id = $1::uuid ORDER BY occurred_at, id`, st.AccountID)
	if err != nil {
		return nil, fmt.Errorf("transitionstore: load signals of %s: %w", st.AccountID, err)
	}
	defer rows.Close()
	situation := knowledge.FromAccountState(st, st.AsOf.UTC(), nil, "", nil)
	var out []transitions.Signal
	for rows.Next() {
		var s transitions.Signal
		var opp string
		var details, refs []byte
		if err := rows.Scan(&s.ID, &s.SignalType, &opp, &s.SubjectPersonID, &details, &refs, &s.OccurredAt); err != nil {
			return nil, fmt.Errorf("transitionstore: scan signal: %w", err)
		}
		if err := json.Unmarshal(refs, &s.EvidenceRefs); err != nil {
			return nil, fmt.Errorf("transitionstore: signal %s evidence: %w", s.ID, err)
		}
		s.OccurredAt, s.OpportunityID = s.OccurredAt.UTC(), opp
		s.Open = knowledge.SignalOpen(knowledgeSignal(s, opp, details), situation)
		out = append(out, s)
	}
	return out, rows.Err()
}

// claimStandings maps claim ids to their standing, so a signal derived from enrichment alone can be told apart.
// Only claims that may earn a state are returned, so a signal that cites a discarded or suggestion-grade claim is not first-party.
func claimStandings(ctx context.Context, q Querier, st reducer.AccountState, rules transitions.RuleSet, ids []string) (map[string]string, error) {
	out := map[string]string{}
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if uuidPattern.MatchString(id) { // a malformed id in a signal must not break the recompute
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT c.id::text, c.standing FROM claims c WHERE c.id = ANY(string_to_array($1, ',')::uuid[]) AND `+eligibleSQL(2, 3, 4),
		strings.Join(valid, ","), strings.Join(rules.ClaimStatuses, ","), st.AsOf.UTC(), claims.MinAIConfidence)
	if err != nil {
		return nil, fmt.Errorf("transitionstore: load standings of cited claims: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, standing string
		if err := rows.Scan(&id, &standing); err != nil {
			return nil, fmt.Errorf("transitionstore: scan standing: %w", err)
		}
		out[id] = standing
	}
	return out, rows.Err()
}

func knowledgeSignal(s transitions.Signal, opportunityID string, details []byte) knowledge.Signal {
	ks := knowledge.Signal{ID: s.ID, Type: s.SignalType, CreatedAt: s.OccurredAt}
	if opportunityID != "" {
		ks.OpportunityID = &opportunityID
	}
	var d map[string]any
	if json.Unmarshal(details, &d) == nil {
		ks.Details = d
		if id, ok := d["subject_claim_id"].(string); ok && id != "" {
			ks.SubjectClaimID = &id
		}
		ks.SubjectItemKey, _ = d["subject_item_key"].(string)
	}
	return ks
}

// patchState writes the relationship state and the open-transition summary onto the state version the
// recompute just wrote (account_state and its state_history row), in the same transaction.
func patchState(ctx context.Context, tx *sql.Tx, st reducer.AccountState, rel Relationship, open *transitions.Record) error {
	relJSON, err := json.Marshal(relationshipOf(rel))
	if err != nil {
		return fmt.Errorf("transitionstore: encode relationship state: %w", err)
	}
	openJSON, err := json.Marshal(openSummary(open))
	if err != nil {
		return fmt.Errorf("transitionstore: encode open transition: %w", err)
	}
	for _, table := range []string{"account_state", "state_history"} {
		res, err := tx.ExecContext(ctx, `UPDATE `+table+` SET state = state || jsonb_build_object('relationship_state', $3::jsonb,
			'open_transition', $4::jsonb) WHERE account_id = $1::uuid AND version = $2`, st.AccountID, st.Version, string(relJSON), string(openJSON))
		if err != nil {
			return fmt.Errorf("transitionstore: expose transition on %s v%d: %w", table, st.Version, err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("transitionstore: %s v%d of %s not found to expose the transition on", table, st.Version, st.AccountID)
		}
	}
	return nil
}

func relationshipOf(rel Relationship) reducer.RelationshipState {
	out := reducer.RelationshipState{Value: rel.State}
	if rel.TransitionID != "" {
		id := rel.TransitionID
		out.TransitionID, out.ConfirmedAt = &id, rel.ConfirmedAt
	}
	return out
}

// openSummary is the AccountState view of an open transition: every unmet fact with its required flag.
func openSummary(open *transitions.Record) *reducer.OpenTransition {
	if open == nil {
		return nil
	}
	missing := make([]reducer.MissingFact, 0, len(open.MissingFacts))
	for _, f := range open.MissingFacts {
		missing = append(missing, reducer.MissingFact{Key: f.Key, Required: f.Required})
	}
	sort.SliceStable(missing, func(i, j int) bool { return missing[i].Required && !missing[j].Required })
	return &reducer.OpenTransition{TransitionID: open.ID, FromState: open.FromState, ToStateCandidate: open.ToStateCandidate,
		Status: open.Status, Confidence: open.Confidence, MissingFacts: missing, LastUpdatedAt: open.LastUpdatedAt}
}
