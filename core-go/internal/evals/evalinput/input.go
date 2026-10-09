// Package evalinput assembles deterministic_eval_input.v1.json (ADR-0014): the database lookups the pure
// deterministic evals never make. One assembly serves candidate evaluation at generation (the orchestrator)
// and the send-time re-evaluation of the human's final artifact (the strategy store, HAR-139). Both judge
// the artifact at the run's replay clock — the newest trigger activity's time — for the world-timed reads
// (replay clock, account state, prior sends); identity-bearing reads (people, opportunities, cited
// activities, run steps) are read live, so rows created or merged between generation and send are visible
// to the send-time check — deliberate for recipient/activity validity, and never evidence-time data.
package evalinput

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// priorSendWindow is how far back prior sends are read for the duplicate-action eval (DuplicateWindow, ADR-0014).
const priorSendWindow = deterministic.DuplicateWindow

// Run is the part of an agent_run row the input assembly reads.
type Run struct {
	ID, AccountID, Mode string
	OpportunityID       string // "" when the run has none
	TriggerIDs          []string
}

// Params is the workspace configuration the evals read: the dry-run policy and the CRM stage rules.
// Params fields mirror orchestrator.Config's; Defaults supplies what an unconfigured workspace uses.
type Params struct {
	WorkspaceID string
	Policy      deterministic.Policy
	CRM         deterministic.CRMRules
}

// Defaults returns the parameters of a workspace without explicit configuration (dry-run policy: every
// tool allowed, customer-facing autonomy — nothing is ever sent; a generic ordered CRM stage list).
// This is the one source of the unconfigured-workspace defaults: orchestrator.DefaultPolicy/DefaultCRM
// delegate to it so a candidate and its send-time re-evaluation always see the same rules (HAR-139).
func Defaults(workspaceID string) Params {
	return Params{
		WorkspaceID: workspaceID,
		Policy: deterministic.Policy{WorkspaceID: workspaceID, AutonomyLevel: "customer_facing", AllowedTools: []string{
			deterministic.ToolEmailSend, deterministic.ToolCalendarInvite, deterministic.ToolDocumentShare,
			deterministic.ToolSlackPost, deterministic.ToolCRMNote, deterministic.ToolCRMUpdate}},
		CRM: deterministic.CRMRules{Stages: []string{"Discovery", "Technical evaluation", "Commercial review", "Negotiation"},
			TerminalStages: []string{"Closed won", "Closed lost"}, MaxForwardSteps: 1},
	}
}

// Normalize fills empty Policy/CRM with the workspace's defaults, so a partially configured Params still evaluates.
func (p Params) Normalize(workspaceID string) Params {
	if p.WorkspaceID == "" {
		p.WorkspaceID = workspaceID
	}
	d := Defaults(p.WorkspaceID)
	if len(p.Policy.AllowedTools) == 0 {
		p.Policy = d.Policy
	}
	if len(p.CRM.Stages) == 0 {
		p.CRM = d.CRM
	}
	p.Policy.WorkspaceID = p.WorkspaceID
	return p
}

// ReplayClock is the run's replay clock: the newest trigger activity's time (invariant I3 — the wall clock
// never enters an evaluation). It errors when none of the trigger activities exists.
func ReplayClock(ctx context.Context, db claimstore.DB, triggerIDs []string) (time.Time, error) {
	var at sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT max(occurred_at) FROM activities WHERE id = ANY($1::uuid[])`,
		signalstore.UUIDArray(triggerIDs)).Scan(&at); err != nil {
		return time.Time{}, fmt.Errorf("evalinput: read trigger time: %w", err)
	}
	if !at.Valid {
		return time.Time{}, errors.New("evalinput: none of the run's trigger activities exists, so there is no replay clock")
	}
	return at.Time.UTC(), nil
}

// State is the account's state at the replay clock (WorldAsOf basis, ADR-0019).
func State(ctx context.Context, db claimstore.DB, accountID string, at time.Time) (reducer.AccountState, bool, error) {
	return coalesce.StateAt(ctx, db, accountID, at, coalesce.WorldAsOf)
}

// Assemble builds the Input of one draft version of the run at the replay clock `at` with account state `state`:
// the run steps, people, opportunities, cited activities and prior sends the evals read. draft is the artifact
// under evaluation — a generated candidate, or the human's final artifact at send time (HAR-139) — and
// draftIndex keeps its EvalResults joinable to that draft. Asset library and commercial context are not
// stored yet: they are empty (their checks pass vacuously) until those tables exist.
func Assemble(ctx context.Context, db claimstore.DB, run Run, draft deterministic.Output, draftIndex int,
	at time.Time, state reducer.AccountState, p Params) (deterministic.Input, error) {
	p = p.Normalize(p.WorkspaceID)
	in := deterministic.Input{
		AgentRunID: run.ID, DraftIndex: draftIndex, WorkspaceID: p.WorkspaceID, AccountID: run.AccountID,
		RunMode: run.Mode, ExecuteMode: "record_only", EvaluatedAt: at, Draft: draft,
		State: state, CRM: p.CRM, Policy: p.Policy,
		Assets: []deterministic.Asset{}, Commercial: deterministic.Commercial{Catalog: []deterministic.CatalogItem{},
			Quoted: []deterministic.QuotedLine{}, ApprovedDiscountPercents: []float64{}},
	}
	if run.OpportunityID != "" {
		in.OpportunityID = &run.OpportunityID
	}
	var err error
	if in.RunSteps, err = Steps(ctx, db, run.ID); err != nil {
		return in, err
	}
	if in.People, err = People(ctx, db, run.AccountID); err != nil {
		return in, err
	}
	if in.Opportunities, err = Opportunities(ctx, db, run.AccountID); err != nil {
		return in, err
	}
	cited := append([]string(nil), run.TriggerIDs...)
	for _, e := range draft.EvidenceRefs {
		cited = appendUnique(cited, e.ActivityID)
	}
	if in.Activities, err = Activities(ctx, db, run.AccountID, cited); err != nil {
		return in, err
	}
	in.PriorActions, err = PriorSends(ctx, db, run.AccountID, at)
	return in, err
}

func appendUnique(xs []string, x string) []string {
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}
