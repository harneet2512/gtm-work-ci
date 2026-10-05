package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// EpisodeSummary is episode_summary.v1.json.
type EpisodeSummary struct {
	ID                string          `json:"id"`
	AgentRunID        string          `json:"agent_run_id"`
	AccountID         string          `json:"account_id"`
	AccountName       string          `json:"account_name"`
	OpportunityID     *string         `json:"opportunity_id"`
	StrategySetID     *string         `json:"strategy_set_id"`
	Status            string          `json:"status"`
	FinalStatus       string          `json:"final_status"`
	TriggeringEvent   *TriggerEvent   `json:"triggering_event"`
	Run               EpisodeRun      `json:"run"`
	RecommendedAction *ActionRef      `json:"recommended_action"`
	SelectedAction    *ActionRef      `json:"selected_action"`
	HumanOutcome      *HumanOutcome   `json:"human_outcome"`
	JudgmentStatus    string          `json:"judgment_status"`
	Replay            *ReplayPosition `json:"replay"`
	CreatedAt         time.Time       `json:"created_at"`
}

// TriggerEvent is the first trigger activity of the run.
type TriggerEvent struct {
	ActivityID           string    `json:"activity_id"`
	SourceEventID        string    `json:"source_event_id"`
	ActivityType         string    `json:"activity_type"`
	SourceSystem         string    `json:"source_system"`
	OccurredAt           time.Time `json:"occurred_at"`
	Summary              *string   `json:"summary"`
	TriggerActivityCount int       `json:"trigger_activity_count"`
}

// EpisodeRun is the run behind the episode and where its generation stands.
type EpisodeRun struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	Phase        string  `json:"phase"`
	StateVersion *int    `json:"state_version"`
	Model        *string `json:"model"`
}

// ActionRef names one strategy candidate.
type ActionRef struct {
	CandidateID      string `json:"candidate_id"`
	Ranking          int    `json:"ranking"`
	StrategyType     string `json:"strategy_type"`
	Title            string `json:"title"`
	ActionType       string `json:"action_type"`
	PreferredByAgent bool   `json:"preferred_by_agent"`
}

// HumanOutcome is what the human did with the strategy set.
type HumanOutcome struct {
	Agreement     string     `json:"agreement"`
	HumanAction   *string    `json:"human_action"`
	SendDecision  string     `json:"send_decision"`
	Edited        bool       `json:"edited"`
	ActorLabel    string     `json:"actor_label"`
	ChosenAt      time.Time  `json:"chosen_at"`
	SendDecidedAt *time.Time `json:"send_decided_at"`
}

// ReplayPosition is where the episode sits in a demo replay.
type ReplayPosition struct {
	ManifestID string `json:"manifest_id"`
	Position   int    `json:"position"`
}

// episodeRow is the header of an episode as stored: what the summary, the trace and the other episode reads share.
type episodeRow struct {
	ID, RunID, AccountID, AccountName string
	OpportunityID                     *string
	Status                            string
	StateVersion                      int
	RunMode                           string // agent_runs.run_mode: dry_run | live
	AccountChangeID, BIUpdateID       *string
	StateDiffID, GuidanceID           *string
	HumanAction                       *string
	HumanDecisionID, HumanDeltaID     *string
	LearningScope                     string
	CreatedAt                         time.Time
}

// loadEpisodeRow reads the episode header. ErrNotFound: malformed id or no such episode.
func loadEpisodeRow(ctx context.Context, db claimstore.DB, id string) (episodeRow, error) {
	if err := requireID("episode", id); err != nil {
		return episodeRow{}, err
	}
	var e episodeRow
	err := db.QueryRowContext(ctx, `SELECT de.id::text, de.agent_run_id::text, de.account_id::text, a.name, ar.opportunity_id::text, de.status,
 de.state_version, ar.run_mode, de.account_change_id::text, de.business_intelligence_update_id::text, de.state_diff_id::text, de.decision_guidance_id::text,
 de.human_action, de.human_decision_id::text, de.human_delta_id::text, de.learning_scope, de.created_at
 FROM decision_episodes de JOIN accounts a ON a.id = de.account_id JOIN agent_runs ar ON ar.id = de.agent_run_id
 WHERE de.id = $1::uuid`, id).Scan(&e.ID, &e.RunID, &e.AccountID, &e.AccountName, &e.OpportunityID, &e.Status, &e.StateVersion, &e.RunMode,
		&e.AccountChangeID, &e.BIUpdateID, &e.StateDiffID, &e.GuidanceID, &e.HumanAction, &e.HumanDecisionID, &e.HumanDeltaID,
		&e.LearningScope, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return episodeRow{}, notFound("episode")
	}
	if err != nil {
		return episodeRow{}, fmt.Errorf("controlplane: read episode: %w", err)
	}
	e.CreatedAt = e.CreatedAt.UTC()
	return e, nil
}

// Episode returns the EpisodeSummary of a DecisionEpisode. ErrNotFound: no such episode.
func (r *Reader) Episode(ctx context.Context, id string) (EpisodeSummary, error) {
	var out EpisodeSummary
	err := r.snapshot(ctx, func(db claimstore.DB) error {
		var err error
		out, err = buildSummary(ctx, db, id)
		return err
	})
	return out, err
}

func buildSummary(ctx context.Context, db claimstore.DB, id string) (EpisodeSummary, error) {
	e, err := loadEpisodeRow(ctx, db, id)
	if err != nil {
		return EpisodeSummary{}, err
	}
	s := EpisodeSummary{ID: e.ID, AgentRunID: e.RunID, AccountID: e.AccountID, AccountName: e.AccountName, OpportunityID: e.OpportunityID,
		Status: e.Status, CreatedAt: e.CreatedAt}
	if s.Run, err = loadEpisodeRun(ctx, db, e.RunID); err != nil {
		return EpisodeSummary{}, err
	}
	if s.TriggeringEvent, err = loadTriggeringEvent(ctx, db, e.RunID); err != nil {
		return EpisodeSummary{}, err
	}
	cands, err := loadCandidates(ctx, db, e.ID)
	if err != nil {
		return EpisodeSummary{}, err
	}
	s.StrategySetID = cands.setID
	if len(cands.byRank) > 0 {
		first := cands.byRank[0].ref
		s.RecommendedAction = &first
	}
	dec, err := loadStrategyDecision(ctx, db, e.ID)
	if err != nil {
		return EpisodeSummary{}, err
	}
	if dec != nil {
		s.HumanOutcome = dec.outcome(e.HumanAction)
		if c := cands.byID[dec.selectedID]; c != nil {
			sel := c.ref
			s.SelectedAction = &sel
		}
	}
	s.FinalStatus = finalStatus(e.Status, e.RunMode, dec)
	if s.JudgmentStatus, err = loadJudgmentStatus(ctx, db, e.ID); err != nil {
		return EpisodeSummary{}, err
	}
	s.Replay, err = loadReplayPosition(ctx, db, e.ID)
	return s, err
}

func loadEpisodeRun(ctx context.Context, db claimstore.DB, runID string) (EpisodeRun, error) {
	var run EpisodeRun
	var runErr *string
	if err := db.QueryRowContext(ctx, `SELECT id::text, status, state_version, model, error FROM agent_runs WHERE id = $1::uuid`, runID).
		Scan(&run.ID, &run.Status, &run.StateVersion, &run.Model, &runErr); err != nil {
		return EpisodeRun{}, fmt.Errorf("controlplane: read run: %w", err)
	}
	g, err := readmodel.LoadGeneration(ctx, db, runID, run.Status, runErr)
	if err != nil {
		return EpisodeRun{}, err
	}
	run.Phase = g.Phase
	return run, nil
}

// loadTriggeringEvent is the earliest trigger activity of the run (nil when the run's activities are gone).
func loadTriggeringEvent(ctx context.Context, db claimstore.DB, runID string) (*TriggerEvent, error) {
	var t TriggerEvent
	err := db.QueryRowContext(ctx, `SELECT a.id::text, a.source_event_id::text, a.activity_type, a.source_system, a.occurred_at, a.summary,
 cardinality(ar.trigger_activity_ids)
 FROM agent_runs ar JOIN activities a ON a.id = ANY(ar.trigger_activity_ids)
 WHERE ar.id = $1::uuid ORDER BY a.occurred_at, a.id LIMIT 1`, runID).
		Scan(&t.ActivityID, &t.SourceEventID, &t.ActivityType, &t.SourceSystem, &t.OccurredAt, &t.Summary, &t.TriggerActivityCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: read trigger activity: %w", err)
	}
	t.OccurredAt = t.OccurredAt.UTC()
	return &t, nil
}

type candidateRow struct {
	ref       ActionRef
	draft     int
	bundleID  string
	knowledge []string
}

// candidateSet is the strategy set of an episode: its id and the candidates by ranking and by id.
type candidateSet struct {
	setID  *string
	byRank []*candidateRow
	byID   map[string]*candidateRow
}

func loadCandidates(ctx context.Context, db claimstore.DB, episodeID string) (candidateSet, error) {
	cs := candidateSet{byID: map[string]*candidateRow{}}
	rows, err := db.QueryContext(ctx, `SELECT ss.id::text, c.id::text, c.ranking, c.strategy_type, c.title, c.action_type, c.preferred_by_agent,
 c.draft_index, c.eval_bundle_id::text, to_jsonb(c.knowledge_refs)
 FROM strategy_sets ss JOIN strategy_candidates c ON c.strategy_set_id = ss.id
 WHERE ss.decision_episode_id = $1::uuid ORDER BY c.ranking`, episodeID)
	if err != nil {
		return cs, fmt.Errorf("controlplane: read candidates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var set string
		var c candidateRow
		var knowledge []byte
		if err := rows.Scan(&set, &c.ref.CandidateID, &c.ref.Ranking, &c.ref.StrategyType, &c.ref.Title, &c.ref.ActionType,
			&c.ref.PreferredByAgent, &c.draft, &c.bundleID, &knowledge); err != nil {
			return cs, fmt.Errorf("controlplane: scan candidate: %w", err)
		}
		if c.knowledge, err = decodeIDs(knowledge); err != nil {
			return cs, err
		}
		cs.setID = &set
		cs.byRank = append(cs.byRank, &c)
		cs.byID[c.ref.CandidateID] = &c
	}
	return cs, rows.Err()
}

type strategyDecision struct {
	id, selectedID, preferredID, actor, sendDecision string
	humanDecisionID                                  *string
	edited                                           bool
	chosenAt                                         time.Time
	sendDecidedAt                                    *time.Time
}

func (d *strategyDecision) outcome(humanAction *string) *HumanOutcome {
	agreement := "overrode"
	if d.selectedID == d.preferredID {
		agreement = "agreed"
	}
	at := d.sendDecidedAt
	if at != nil {
		u := at.UTC()
		at = &u
	}
	return &HumanOutcome{Agreement: agreement, HumanAction: humanAction, SendDecision: d.sendDecision, Edited: d.edited,
		ActorLabel: d.actor, ChosenAt: d.chosenAt.UTC(), SendDecidedAt: at}
}

// loadStrategyDecision is the human's choice for the episode (nil until they choose).
func loadStrategyDecision(ctx context.Context, db claimstore.DB, episodeID string) (*strategyDecision, error) {
	var d strategyDecision
	err := db.QueryRowContext(ctx, `SELECT id::text, selected_candidate_id::text, original_agent_preference::text, actor_label, send_decision,
 jsonb_array_length(edits) > 0, chosen_at, send_decided_at, human_decision_id::text
 FROM human_strategy_decisions WHERE decision_episode_id = $1::uuid`, episodeID).
		Scan(&d.id, &d.selectedID, &d.preferredID, &d.actor, &d.sendDecision, &d.edited, &d.chosenAt, &d.sendDecidedAt, &d.humanDecisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: read strategy decision: %w", err)
	}
	return &d, nil
}

// finalStatus is how far the human side got: the send decision when there is a choice, else the episode stage. A send
// decision on a dry run is recorded, not performed (the executor of a dry run never sends), so it is send_recorded; only
// a live run says sent.
func finalStatus(status, runMode string, d *strategyDecision) string {
	switch {
	case d != nil && d.sendDecision == "send" && runMode == "live":
		return "sent"
	case d != nil && d.sendDecision == "send":
		return "send_recorded"
	case d != nil && d.sendDecision == "discard":
		return "discarded"
	case d != nil:
		return "awaiting_send"
	case status == "decided" || status == "judged":
		return "decided"
	case status == "chosen":
		return "awaiting_send"
	default:
		return "awaiting_choice"
	}
}

func loadJudgmentStatus(ctx context.Context, db claimstore.DB, episodeID string) (string, error) {
	var verdict string
	err := db.QueryRowContext(ctx, `SELECT human_verdict FROM judgment_inferences WHERE decision_episode_id = $1::uuid`, episodeID).Scan(&verdict)
	if errors.Is(err, sql.ErrNoRows) {
		return "none", nil
	}
	if err != nil {
		return "", fmt.Errorf("controlplane: read judgment inference: %w", err)
	}
	return verdict, nil
}

func loadReplayPosition(ctx context.Context, db claimstore.DB, episodeID string) (*ReplayPosition, error) {
	var p ReplayPosition
	err := db.QueryRowContext(ctx, `SELECT manifest_id::text, position FROM demo_episodes WHERE decision_episode_id = $1::uuid
 ORDER BY released_at DESC, manifest_id, position LIMIT 1`, episodeID).Scan(&p.ManifestID, &p.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: read replay position: %w", err)
	}
	return &p, nil
}
