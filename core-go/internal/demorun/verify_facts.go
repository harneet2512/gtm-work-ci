package demorun

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// FetchOptions tune FetchFacts.
type FetchOptions struct {
	SlackOn   bool
	AuditPath string
}

// FetchFacts reads the authoritative rows. Identity comes from the database, not from the remembered state: the
// newest BI update and strategy set of the account, then everything keyed by that episode.
func FetchFacts(ctx context.Context, db *sql.DB, st DemoState, o FetchOptions) (Facts, error) {
	f := Facts{ManifestID: st.ManifestID, InvisibilityAtSeed: st.InvisibilityAtSeed, Played: !st.PlayedAt.IsZero(), SlackOn: o.SlackOn, RunID: st.RunID}
	var err error
	if f.BIUpdateID, err = optString(ctx, db, `SELECT id::text FROM business_intelligence_updates WHERE account_id = $1::uuid ORDER BY created_at DESC LIMIT 1`, st.AccountID); err != nil {
		return f, fmt.Errorf("read the BI update: %w", err)
	}
	var run, set, ep sql.NullString
	switch err := db.QueryRowContext(ctx, `SELECT id::text, agent_run_id::text, decision_episode_id::text FROM strategy_sets WHERE account_id = $1::uuid ORDER BY created_at DESC LIMIT 1`, st.AccountID).Scan(&set, &run, &ep); {
	case err == sql.ErrNoRows:
	case err != nil:
		return f, fmt.Errorf("read the strategy set: %w", err)
	default:
		f.StrategySetID, f.RunID, f.EpisodeID = set.String, run.String, ep.String
	}
	subjects := []string{}
	if f.BIUpdateID != "" {
		subjects = append(subjects, f.BIUpdateID)
		if f.M1TS, err = surfaceTS(ctx, db, f.BIUpdateID, "bi"); err != nil {
			return f, err
		}
	}
	if f.EpisodeID != "" {
		subjects = append(subjects, f.EpisodeID)
		if f.M2TS, err = surfaceTS(ctx, db, f.EpisodeID, "chooser"); err != nil {
			return f, err
		}
		if f.M3TS, err = surfaceTS(ctx, db, f.EpisodeID, "judgment"); err != nil {
			return f, err
		}
		if err := fetchSupervision(ctx, db, f.EpisodeID, &f); err != nil {
			return f, err
		}
	}
	if len(subjects) > 0 {
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM surface_messages WHERE surface = 'slack' AND ts IS NOT NULL AND subject_id = ANY($1::uuid[])`,
			"{"+strings.Join(subjects, ",")+"}").Scan(&f.SlackRowsPosted); err != nil {
			return f, fmt.Errorf("count surface messages: %w", err)
		}
	}
	if o.AuditPath != "" {
		f.AuditPosts, f.AuditUpdates, f.AuditKnown = CountAudit(o.AuditPath)
	}
	return f, nil
}

func fetchSupervision(ctx context.Context, db *sql.DB, episode string, f *Facts) error {
	var d DecisionFact
	var hd, ha sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT hsd.id::text, hsd.selected_candidate_id::text, hsd.original_agent_preference::text, hsd.send_decision,
		       jsonb_array_length(hsd.edits), hd.decision, de.human_action
		FROM human_strategy_decisions hsd
		LEFT JOIN human_decisions hd ON hd.id = hsd.human_decision_id
		LEFT JOIN decision_episodes de ON de.id = hsd.decision_episode_id
		WHERE hsd.decision_episode_id = $1::uuid`, episode).Scan(&d.ID, &d.SelectedCandidateID, &d.OriginalPreference, &d.SendDecision, &d.Edits, &hd, &ha)
	switch {
	case err == sql.ErrNoRows:
	case err != nil:
		return fmt.Errorf("read the human decision: %w", err)
	default:
		d.HumanDecision, d.HumanAction = hd.String, ha.String
		f.Decision = &d
	}
	var delta DeltaFact
	var labels sql.NullString
	err = db.QueryRowContext(ctx, `SELECT id::text, array_to_string(semantic_labels, ','), unexplained, jsonb_array_length(literal_changes) FROM human_deltas WHERE decision_episode_id = $1::uuid`, episode).
		Scan(&delta.ID, &labels, &delta.Unexplained, &delta.LiteralChanges)
	switch {
	case err == sql.ErrNoRows:
	case err != nil:
		return fmt.Errorf("read the human delta: %w", err)
	default:
		if labels.String != "" {
			delta.Labels = strings.Split(labels.String, ",")
		}
		f.Delta = &delta
	}
	var inf InferenceFact
	err = db.QueryRowContext(ctx, `SELECT id::text, human_verdict, corrected_statement IS NOT NULL FROM judgment_inferences WHERE decision_episode_id = $1::uuid`, episode).
		Scan(&inf.ID, &inf.Verdict, &inf.HasCorrection)
	switch {
	case err == sql.ErrNoRows:
		return nil
	case err != nil:
		return fmt.Errorf("read the judgment inference: %w", err)
	}
	f.Inference = &inf
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM judgment_verdicts WHERE judgment_inference_id = $1::uuid`, inf.ID).Scan(&f.VerdictRows); err != nil {
		return fmt.Errorf("count judgment verdicts: %w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM judgment_notes WHERE judgment_inference_id = $1::uuid`, inf.ID).Scan(&f.NoteRows); err != nil {
		return fmt.Errorf("count judgment notes: %w", err)
	}
	return nil
}

func optString(ctx context.Context, db *sql.DB, query string, args ...any) (string, error) {
	var s sql.NullString
	err := db.QueryRowContext(ctx, query, args...).Scan(&s)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return s.String, err
}

func surfaceTS(ctx context.Context, db *sql.DB, subject, kind string) (string, error) {
	ts, err := optString(ctx, db, `SELECT ts FROM surface_messages WHERE subject_id = $1::uuid AND surface = 'slack' AND kind = $2`, subject, kind)
	if err != nil {
		return "", fmt.Errorf("read the %s message ref: %w", kind, err)
	}
	return ts, nil
}
