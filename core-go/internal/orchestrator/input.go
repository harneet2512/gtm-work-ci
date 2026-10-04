package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// priorSendWindow is how far back prior sends are read for the duplicate-action eval (DuplicateWindow, ADR-0014).
const priorSendWindow = deterministic.DuplicateWindow

// evalInput assembles deterministic_eval_input.v1.json for one candidate version (ADR-0014): the database lookups
// the pure evals never make. EvaluatedAt is the replay clock, so the same run evaluates the same way whenever it
// is replayed. Asset library and commercial context are not stored yet: they are empty (their checks pass
// vacuously) until those tables exist.
func (s *Service) evalInput(ctx context.Context, run runRow, w world, c workerclient.Candidate, draftIndex int) (deterministic.Input, error) {
	in := deterministic.Input{
		AgentRunID: run.ID, DraftIndex: draftIndex, WorkspaceID: s.cfg.WorkspaceID, AccountID: run.AccountID,
		RunMode: run.Mode, ExecuteMode: "record_only", EvaluatedAt: w.EventTime, Draft: draftOutput(c),
		State: w.State, CRM: s.cfg.CRM, Policy: s.cfg.Policy,
		Assets: []deterministic.Asset{}, Commercial: deterministic.Commercial{Catalog: []deterministic.CatalogItem{},
			Quoted: []deterministic.QuotedLine{}, ApprovedDiscountPercents: []float64{}},
	}
	if run.OpportunityID != "" {
		in.OpportunityID = &run.OpportunityID
	}
	var err error
	if in.RunSteps, err = s.runSteps(ctx, run.ID); err != nil {
		return in, err
	}
	if in.People, err = s.people(ctx, run.AccountID); err != nil {
		return in, err
	}
	if in.Opportunities, err = s.opportunities(ctx, run.AccountID); err != nil {
		return in, err
	}
	cited := slices.Clone(run.TriggerIDs)
	for _, e := range c.EvidenceRefs {
		cited = appendUnique(cited, e.ActivityID)
	}
	if in.Activities, err = s.activities(ctx, run.AccountID, cited); err != nil {
		return in, err
	}
	in.PriorActions, err = s.priorSends(ctx, run.AccountID, w.EventTime)
	return in, err
}

func draftOutput(c workerclient.Candidate) deterministic.Output {
	out := deterministic.Output{
		ProposedActionType: c.ActionType,
		FinishedArtifact: deterministic.Artifact{Channel: c.FullActionArtifact.Channel, Subject: c.FullActionArtifact.Subject,
			Body: c.FullActionArtifact.Body, Attachments: c.FullActionArtifact.Attachments},
		CRMNextStepIntent: deterministic.CRMIntent{NextStep: c.Description}, Reason: c.Rationale,
		KnowledgeRefsUsed: c.KnowledgeRefs,
	}
	for _, r := range slices.Concat(c.To, c.CC) {
		out.Recipients = append(out.Recipients, deterministic.Recipient{PersonID: r.PersonID, Role: r.Role, Why: r.Why})
	}
	for _, e := range c.EvidenceRefs {
		out.EvidenceRefs = append(out.EvidenceRefs, deterministic.EvidenceRef{ActivityID: e.ActivityID, ClaimID: e.ClaimID,
			Quote: e.Quote, SpeakerPersonID: e.SpeakerPersonID, OccurredAt: e.OccurredAt})
	}
	if out.Recipients == nil {
		out.Recipients = []deterministic.Recipient{}
	}
	if out.EvidenceRefs == nil {
		out.EvidenceRefs = []deterministic.EvidenceRef{}
	}
	return out
}

func (s *Service) runSteps(ctx context.Context, runID string) ([]deterministic.RunStep, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, step, status, started_at, finished_at FROM agent_run_steps
 WHERE agent_run_id = $1::uuid ORDER BY seq`, runID)
	if err != nil {
		return nil, transient("evaluate", fmt.Errorf("read steps: %w", err))
	}
	defer rows.Close()
	out := []deterministic.RunStep{}
	for rows.Next() {
		var st deterministic.RunStep
		var started, finished sql.NullTime
		if err := rows.Scan(&st.Seq, &st.Step, &st.Status, &started, &finished); err != nil {
			return nil, transient("evaluate", err)
		}
		st.StartedAt, st.FinishedAt = nullTimeUTC(started), nullTimeUTC(finished)
		out = append(out, st)
	}
	return out, rows.Err()
}

func nullTimeUTC(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

func (s *Service) people(ctx context.Context, accountID string) ([]deterministic.Person, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, display_name, kind, account_id::text, merged_into::text,
 primary_email IS NOT NULL, internal_only FROM people WHERE kind = 'employee' OR account_id = $1::uuid ORDER BY id`, accountID)
	if err != nil {
		return nil, transient("evaluate", fmt.Errorf("read people: %w", err))
	}
	defer rows.Close()
	out := []deterministic.Person{}
	for rows.Next() {
		var p deterministic.Person
		var acct, merged sql.NullString
		if err := rows.Scan(&p.PersonID, &p.DisplayName, &p.Kind, &acct, &merged, &p.HasEmail, &p.InternalOnly); err != nil {
			return nil, transient("evaluate", err)
		}
		if acct.Valid {
			p.AccountID = &acct.String
		}
		if merged.Valid {
			p.MergedInto = &merged.String
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) opportunities(ctx context.Context, accountID string) ([]deterministic.Opportunity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, account_id::text, owner_person_id::text FROM opportunities
 WHERE account_id = $1::uuid ORDER BY id`, accountID)
	if err != nil {
		return nil, transient("evaluate", fmt.Errorf("read opportunities: %w", err))
	}
	defer rows.Close()
	out := []deterministic.Opportunity{}
	for rows.Next() {
		var o deterministic.Opportunity
		var owner sql.NullString
		if err := rows.Scan(&o.OpportunityID, &o.AccountID, &owner); err != nil {
			return nil, transient("evaluate", err)
		}
		if owner.Valid {
			o.OwnerPersonID = &owner.String
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Service) activities(ctx context.Context, accountID string, ids []string) ([]deterministic.Activity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, account_id::text, activity_type, occurred_at, COALESCE(body_text, summary, '')
 FROM activities WHERE id = ANY($1::uuid[]) AND account_id = $2::uuid ORDER BY occurred_at, id`, signalstore.UUIDArray(ids), accountID)
	if err != nil {
		return nil, transient("evaluate", fmt.Errorf("read activities: %w", err))
	}
	defer rows.Close()
	out := []deterministic.Activity{}
	for rows.Next() {
		var a deterministic.Activity
		var acct sql.NullString
		if err := rows.Scan(&a.ActivityID, &acct, &a.ActivityType, &a.OccurredAt, &a.Text); err != nil {
			return nil, transient("evaluate", err)
		}
		a.OccurredAt = a.OccurredAt.UTC()
		if acct.Valid {
			a.AccountID = &acct.String
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// priorSends are the account's emails the rep sent in the duplicate window before the replay clock.
func (s *Service) priorSends(ctx context.Context, accountID string, at time.Time) ([]deterministic.PriorAction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id::text, a.occurred_at, a.summary, a.body_text,
   COALESCE((SELECT array_agg(p.person_id::text) FROM activity_participants p WHERE p.activity_id = a.id
             AND p.role IN ('to', 'cc') AND p.person_id IS NOT NULL), '{}')
 FROM activities a WHERE a.account_id = $1::uuid AND a.activity_type = 'EmailSent'
   AND a.occurred_at <= $2 AND a.occurred_at > $3 ORDER BY a.occurred_at, a.id`,
		accountID, at.UTC(), at.UTC().Add(-priorSendWindow))
	if err != nil {
		return nil, transient("evaluate", fmt.Errorf("read prior sends: %w", err))
	}
	defer rows.Close()
	out := []deterministic.PriorAction{}
	for rows.Next() {
		var p deterministic.PriorAction
		var subject, body sql.NullString
		var recipients string
		if err := rows.Scan(&p.RefID, &p.OccurredAt, &subject, &body, &recipients); err != nil {
			return nil, transient("evaluate", err)
		}
		p.RefKind, p.Action, p.Status, p.OccurredAt = "activity", "send_email", "completed", p.OccurredAt.UTC()
		p.RecipientPersonIDs, p.Attachments = parseUUIDArray(recipients), []string{}
		if subject.Valid {
			p.Subject = &subject.String
		}
		if body.Valid {
			p.BodyText = &body.String
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// parseUUIDArray reads a Postgres uuid-array text literal ({a,b}).
func parseUUIDArray(lit string) []string {
	ids := []string{}
	for _, f := range splitList(trimBraces(lit)) {
		ids = append(ids, f)
	}
	return ids
}

func trimBraces(s string) string {
	if len(s) >= 2 && s[0] == '{' && s[len(s)-1] == '}' {
		return s[1 : len(s)-1]
	}
	return s
}
