package ctxgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

const (
	basisOriginEpisode = "origin_episode"
	basisUsedInRun     = "used_in_run"
)

type episodeRow struct {
	id, runID, action string
	stateVersion      int
	stateDiff         sql.NullString
	opportunity       sql.NullString
	triggers, used    []string
	created           time.Time
}

// loadLearning projects the account's decision episodes and the knowledge connected to them:
// knowledge a run used, and knowledge whose evidence is one of the account's episodes. Knowledge
// that no episode of the account touches is not part of this account's graph.
func (b *builder) loadLearning(ctx context.Context) error {
	eps, err := b.readEpisodes(ctx)
	if err != nil || len(eps) == 0 {
		return err
	}
	if b.cut != nil {
		return b.loadEpisodesAtCut(ctx, eps)
	}
	supports, err := b.readKnowledgeEvidence(ctx, eps)
	if err != nil {
		return err
	}
	if err := b.loadKnowledge(ctx, eps, supports); err != nil {
		return err
	}
	for _, e := range eps {
		if err := b.projectEpisode(ctx, e); err != nil {
			return err
		}
	}
	return b.linkKnowledge(eps, supports)
}

// readEpisodes reads the account's episodes. An episode the human has not decided yet (the orchestrator publishes
// it as awaiting_choice, HAR-117) has no human_action: its status stands in, so it is projected, not an error.
func (b *builder) readEpisodes(ctx context.Context) ([]episodeRow, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT de.id::text, de.agent_run_id::text, COALESCE(de.human_action, de.status), de.state_version, de.state_diff_id::text, ar.opportunity_id::text,
       to_jsonb(ar.trigger_activity_ids)::text, ar.knowledge_refs_used::text, de.created_at
  FROM decision_episodes de JOIN agent_runs ar ON ar.id = de.agent_run_id
 WHERE de.account_id = $1::uuid ORDER BY de.id`, b.accountID)
	if err != nil {
		return nil, fmt.Errorf("ctxgraph: read decision episodes: %w", err)
	}
	defer rows.Close()
	var out []episodeRow
	for rows.Next() {
		var e episodeRow
		var triggers, used string
		if err := rows.Scan(&e.id, &e.runID, &e.action, &e.stateVersion, &e.stateDiff, &e.opportunity, &triggers, &used, &e.created); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(triggers), &e.triggers); err != nil {
			return nil, fmt.Errorf("ctxgraph: episode %s trigger ids: %w", e.id, err)
		}
		if err := json.Unmarshal([]byte(used), &e.used); err != nil {
			return nil, fmt.Errorf("ctxgraph: episode %s knowledge_refs_used: %w", e.id, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// supportRow is one knowledge_evidence row whose ref is a decision episode of the account.
type supportRow struct {
	id, knowledgeID, episodeID string
	created                    time.Time
}

func (b *builder) readKnowledgeEvidence(ctx context.Context, eps []episodeRow) ([]supportRow, error) {
	ids := make([]string, len(eps))
	for i, e := range eps {
		ids[i] = e.id
	}
	rows, err := b.db.QueryContext(ctx, `
SELECT id::text, knowledge_id::text, ref_id::text, created_at FROM knowledge_evidence
 WHERE kind = 'decision_episode' AND ref_id = ANY($1::uuid[]) ORDER BY id`, signalstore.UUIDArray(ids))
	if err != nil {
		return nil, fmt.Errorf("ctxgraph: read knowledge evidence: %w", err)
	}
	defer rows.Close()
	var out []supportRow
	for rows.Next() {
		var s supportRow
		if err := rows.Scan(&s.id, &s.knowledgeID, &s.episodeID, &s.created); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (b *builder) loadKnowledge(ctx context.Context, eps []episodeRow, supports []supportRow) error {
	want := map[string]bool{}
	for _, e := range eps {
		for _, k := range e.used {
			want[k] = true
		}
	}
	for _, s := range supports {
		want[s.knowledgeID] = true
	}
	if len(want) == 0 {
		return nil
	}
	ids := make([]string, 0, len(want))
	for k := range want {
		ids = append(ids, k)
	}
	rows, err := b.db.QueryContext(ctx, `
SELECT id::text, key, title, status, support_count, created_at, last_validated_at FROM knowledge WHERE id = ANY($1::uuid[])`,
		signalstore.UUIDArray(ids))
	if err != nil {
		return fmt.Errorf("ctxgraph: read knowledge: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, key, title, status string
		var count int
		var created time.Time
		var validated sql.NullTime
		if err := rows.Scan(&id, &key, &title, &status, &count, &created, &validated); err != nil {
			return err
		}
		updated := created
		extra := map[string]any{"key": key, "title": clip(title, 300), "status": status, "support_count": int64(count)}
		if validated.Valid {
			updated = validated.Time
			extra["last_validated_at"] = ts(validated.Time)
		}
		b.addNode(newNode([]string{LabelKnowledge}, id, "", nodeProps("knowledge", []string{}, []string{}, created, updated, extra)))
	}
	return rows.Err()
}

func (b *builder) projectEpisode(ctx context.Context, e episodeRow) error {
	acts, events, err := b.evidence(ctx, e.triggers...)
	if err != nil {
		return err
	}
	extra := map[string]any{"state_version": int64(e.stateVersion), "valid_from": ts(e.created), "agent_run_id": e.runID}
	if e.action != "" { // a world read carries no verdict or status (loadEpisodesAtCut)
		extra["human_action"] = e.action
	}
	b.addNode(newNode([]string{LabelDecisionEpisode}, e.id, b.accountID, nodeProps("decision_episodes", acts, events, e.created, e.created, extra)))
	props := edgeProps(e.created, nil, statusRecorded, acts, events, e.created, e.created, nil)
	edge := func(typ, toLabel, toID string) {
		b.addEdge(newEdge(typ, "episode:"+e.id+":"+typ+":"+toID, LabelDecisionEpisode, e.id, toLabel, toID, b.accountID, props))
	}
	edge(RelAboutAccount, LabelAccount, b.accountID)
	if e.opportunity.Valid {
		edge(RelAboutOpportunity, LabelOpportunity, e.opportunity.String)
	}
	for _, a := range acts {
		if info, ok := b.acts[a]; ok {
			edge(RelTriggeredBy, info.label, a)
		}
	}
	if e.stateDiff.Valid {
		for _, sig := range b.diffSignals[e.stateDiff.String] {
			edge(RelTriggeredBy, LabelSignal, sig)
		}
	}
	for _, k := range e.used {
		edge(RelUsedKnowledge, LabelKnowledge, k)
	}
	return nil
}

// linkKnowledge adds Knowledge SUPPORTED_BY DecisionEpisode and Knowledge APPLIES_TO Account/Opportunity.
// APPLIES_TO records the basis: the knowledge was learned from an episode of the account (origin_episode)
// or a run on the account used it (used_in_run). Whether it applies to a given situation is the
// applicability matcher's judgement in Postgres, not something the graph decides.
func (b *builder) linkKnowledge(eps []episodeRow, supports []supportRow) error {
	byID := map[string]episodeRow{}
	for _, e := range eps {
		byID[e.id] = e
	}
	type key struct{ knowledge, basis, label, target string }
	first := map[key]time.Time{}
	note := func(k key, at time.Time) {
		if cur, ok := first[k]; !ok || at.Before(cur) {
			first[k] = at
		}
	}
	for _, s := range supports {
		e := byID[s.episodeID]
		props := edgeProps(s.created, nil, statusRecorded, []string{}, []string{}, s.created, s.created, nil)
		b.addEdge(newEdge(RelSupportedBy, "ke:"+s.id, LabelKnowledge, s.knowledgeID, LabelDecisionEpisode, s.episodeID, b.accountID, props))
		note(key{s.knowledgeID, basisOriginEpisode, LabelAccount, b.accountID}, e.created)
		if e.opportunity.Valid {
			note(key{s.knowledgeID, basisOriginEpisode, LabelOpportunity, e.opportunity.String}, e.created)
		}
	}
	for _, e := range eps {
		for _, k := range e.used {
			note(key{k, basisUsedInRun, LabelAccount, b.accountID}, e.created)
			if e.opportunity.Valid {
				note(key{k, basisUsedInRun, LabelOpportunity, e.opportunity.String}, e.created)
			}
		}
	}
	keys := make([]key, 0, len(first))
	for k := range first {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j])
	})
	for _, k := range keys {
		at := first[k]
		props := edgeProps(at, nil, statusRecorded, []string{}, []string{}, at, at, map[string]any{"basis": k.basis})
		b.addEdge(newEdge(RelAppliesTo, "k:"+k.knowledge+":APPLIES_TO:"+k.basis+":"+k.target, LabelKnowledge, k.knowledge, k.label, k.target, b.accountID, props))
	}
	return nil
}
