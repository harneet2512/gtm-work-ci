package bucket1load

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
	"github.com/harneet2512/gtm-work/core-go/internal/controlplane"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// opPriority orders the operations of several mutations of one knowledge object in one episode: a status
// consequence (dispute, stale) names the revision over the evidence that caused it.
var opPriority = map[string]int{"DISPUTE": 5, "MARK_STALE": 4, "CREATE": 3, "WEAKEN": 2, "STRENGTHEN": 1}

// reactionPolarity: the polarity of a customer reaction type (positive moves the conversation forward).
var reactionPolarity = map[string]string{
	"replied": "positive", "meeting_accepted": "positive", "stakeholder_added": "positive", "new_commitment": "positive",
	"requested_document": "positive", "conversation_advanced": "positive",
	"ignored": "negative", "objection_raised": "negative", "conversation_cooled": "negative", "stakeholder_removed": "negative",
}

// loadMutations reads the knowledge the episode changed (the control plane's own derivation from the lifecycle
// records), groups it into one Revision per knowledge object and builds the trace the revision must be justified
// by: the human's edit as interpreted, the customer evidence kept apart, and where the episode sits against the
// knowledge that existed.
func loadMutations(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) error {
	reader, err := controlplane.New(db)
	if err != nil {
		return fmt.Errorf("bucket1load: control plane reader: %w", err)
	}
	muts, err := reader.KnowledgeMutations(ctx, run.episodeID)
	if err != nil {
		return fmt.Errorf("bucket1load: knowledge mutations of episode %s: %w", run.episodeID, err)
	}
	ep.Revisions = revisionsOf(muts.Items, run.episodeID, ep)
	tr, err := traceOf(ctx, db, run, ep)
	if err != nil {
		return err
	}
	ep.Trace = &tr
	return nil
}

func revisionsOf(items []controlplane.KnowledgeMutation, episodeID string, ep *bucket1.Episode) []bucket1.Revision {
	var order []string
	by := map[string]*bucket1.Revision{}
	n := map[string]int{}
	for _, m := range items {
		r, ok := by[m.KnowledgeID]
		if !ok {
			r = &bucket1.Revision{ID: m.ID, KnowledgeID: m.KnowledgeID, SourceEpisode: episodeID, PriorKept: true,
				Preconditions: conds(m.Preconditions), ApplicabilityConditions: conds(m.ApplicabilityCondition), Exceptions: excs(m.Exceptions)}
			by[m.KnowledgeID] = r
			order = append(order, m.KnowledgeID)
		}
		n[m.KnowledgeID]++
		if opPriority[m.Operation] >= opPriority[r.Operation] {
			r.Operation = m.Operation
		}
		r.Status, r.Version, r.OccurredAt = m.Status, m.Version, ep.At // replay time of the episode, never the wall clock
		r.Evidence = append(r.Evidence, bucket1.RevisionEvidence{Kind: m.Evidence.Kind, RefID: m.Evidence.RefID, ActivityID: firstActivityID(ep)})
	}
	out := make([]bucket1.Revision, 0, len(order))
	for _, id := range order {
		r := by[id]
		r.VersionBefore = r.Version - n[id]
		out = append(out, *r)
	}
	return out
}

func firstActivityID(ep *bucket1.Episode) string {
	if len(ep.Activities) > 0 {
		return ep.Activities[0].ID
	}
	return ""
}

func conds(raw json.RawMessage) []knowledge.Condition {
	var out []knowledge.Condition
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func excs(raw json.RawMessage) []knowledge.Exception {
	var out []knowledge.Exception
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func traceOf(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) (bucket1.RevisionTrace, error) {
	var t bucket1.RevisionTrace
	var strength sql.NullString
	var verdict string
	var classes string
	err := db.QueryRowContext(ctx, `SELECT human_verdict, signal_strength, array_to_string(edit_class, ',') FROM judgment_inferences WHERE decision_episode_id = $1::uuid`,
		run.episodeID).Scan(&verdict, &strength, &classes)
	switch {
	case err == sql.ErrNoRows:
	case err != nil:
		return t, fmt.Errorf("bucket1load: judgment inference of episode %s: %w", run.episodeID, err)
	default:
		t.HumanEdit = classes != ""
		t.SignalStrength = strength.String
	}
	rows, err := db.QueryContext(ctx, `SELECT reaction_type FROM customer_reactions WHERE agent_run_id = $1::uuid`, run.id)
	if err != nil {
		return t, fmt.Errorf("bucket1load: customer reactions of run %s: %w", run.id, err)
	}
	defer rows.Close()
	positive := 0
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return t, fmt.Errorf("bucket1load: scan reaction: %w", err)
		}
		if p, ok := reactionPolarity[kind]; ok {
			t.Customer = append(t.Customer, p)
			if p == "positive" {
				positive++
			}
		}
	}
	t.IndependentPositive = positive
	if a := ep.Attribution; a != nil {
		t.KnowledgeInScope = len(a.Applicable) > 0
		t.KnowledgeOutOfScope = len(a.Retrieved) > len(a.Applicable)+len(a.ExceptionBlocked)
	}
	for _, r := range ep.Revisions {
		t.Disputed = t.Disputed || r.Operation == "DISPUTE"
		t.Stale = t.Stale || r.Operation == "MARK_STALE"
	}
	return t, rows.Err()
}
