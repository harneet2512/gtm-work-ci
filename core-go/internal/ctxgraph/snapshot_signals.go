package ctxgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// statusRecorded is the status of edges that record a fact about the past (they are never open or closed).
const statusRecorded = "recorded"

type signalRow struct {
	id, signalType, rule, evidence              string
	opportunity, subject, subjectClaim, stateDf sql.NullString
	occurred, created                           time.Time
	expires                                     sql.NullTime
}

// signalsByDiff indexes the account's signal ids by the state diff that emitted them; decision episodes
// are TRIGGERED_BY the signals of their diff.
type signalsByDiff map[string][]string

func (b *builder) loadSignals(ctx context.Context) error {
	rows, err := b.db.QueryContext(ctx, `
SELECT id::text, signal_type, rule, evidence_refs::text, opportunity_id::text, subject_person_id::text,
       subject_claim_id::text, state_diff_id::text, occurred_at, created_at, expires_at
  FROM signals WHERE account_id = $1::uuid AND ($2::timestamptz IS NULL OR occurred_at < $2)`, b.accountID, b.cutArg())
	if err != nil {
		return fmt.Errorf("ctxgraph: read signals: %w", err)
	}
	defer rows.Close()
	var all []signalRow
	for rows.Next() {
		var s signalRow
		if err := rows.Scan(&s.id, &s.signalType, &s.rule, &s.evidence, &s.opportunity, &s.subject, &s.subjectClaim,
			&s.stateDf, &s.occurred, &s.created, &s.expires); err != nil {
			return err
		}
		all = append(all, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	b.diffSignals = signalsByDiff{}
	for _, s := range all {
		if b.cut != nil {
			// A signal derived (even in part) from an activity not before the cutoff did not exist yet.
			refs, err := evidenceActivityIDs(s.evidence)
			if err != nil {
				return fmt.Errorf("ctxgraph: signal %s evidence_refs: %w", s.id, err)
			}
			if ok, err := b.allVisible(ctx, refs); err != nil {
				return err
			} else if !ok {
				continue
			}
		}
		if s.stateDf.Valid {
			b.diffSignals[s.stateDf.String] = append(b.diffSignals[s.stateDf.String], s.id)
		}
		if err := b.projectSignal(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) projectSignal(ctx context.Context, s signalRow) error {
	refs, err := evidenceActivityIDs(s.evidence)
	if err != nil {
		return fmt.Errorf("ctxgraph: signal %s evidence_refs: %w", s.id, err)
	}
	acts, events, err := b.evidence(ctx, refs...)
	if err != nil {
		return err
	}
	extra := map[string]any{"signal_type": s.signalType, "rule": s.rule, "valid_from": ts(s.occurred)}
	var validTo *time.Time
	if s.expires.Valid {
		t := s.expires.Time
		validTo = &t
		extra["valid_to"] = ts(t)
	}
	b.addNode(newNode([]string{LabelSignal}, s.id, b.accountID, nodeProps("signals", acts, events, s.created, s.created, extra)))
	props := edgeProps(s.occurred, validTo, statusRecorded, acts, events, s.created, s.created, nil)
	edge := func(typ, toLabel, toID string) {
		b.addEdge(newEdge(typ, "signal:"+s.id+":"+typ+":"+toID, LabelSignal, s.id, toLabel, toID, b.accountID, props))
	}
	edge(RelAboutAccount, LabelAccount, b.accountID)
	if s.opportunity.Valid {
		edge(RelAboutOpportunity, LabelOpportunity, s.opportunity.String)
	}
	if s.subject.Valid {
		edge(RelAboutPerson, LabelPerson, s.subject.String)
	}
	if s.subjectClaim.Valid {
		if label, ok := b.claimLabels[s.subjectClaim.String]; ok {
			edge(RelDerivedFrom, label, s.subjectClaim.String)
		}
	}
	for _, a := range acts {
		if info, ok := b.acts[a]; ok {
			edge(RelDerivedFrom, info.label, a)
		}
	}
	return nil
}

// evidenceActivityIDs reads the activity ids of a signal's evidence_refs (common.v1.json evidenceRef).
func evidenceActivityIDs(raw string) ([]string, error) {
	var refs []struct {
		ActivityID string `json:"activity_id"`
	}
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.ActivityID)
	}
	return out, nil
}
