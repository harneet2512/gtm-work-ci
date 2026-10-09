package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// traceInput is everything the spans are built from, read in one snapshot.
type traceInput struct {
	episode   episodeRow
	triggers  []triggerRow
	change    *changeRow
	graph     *graphRow
	diff      *diffRow
	attr      *attribution
	guidance  []guidanceEntry
	cands     candidateSet
	setAt     *time.Time
	surfaces  []surfaceRow
	decision  *strategyDecision
	inference *inferenceRow
	reactions []string
	mutations []KnowledgeMutation
	results   []result
}

type triggerRow struct {
	id, sourceEventID, activityType, sourceSystem string
	occurredAt                                    time.Time
	summary, accountHint                          *string
	accountID                                     *string
	participants, resolved, mappings              int
}

type changeRow struct {
	id           string
	stateDiffID  string
	evidenceRefs json.RawMessage
	createdAt    time.Time
	material     bool
}

type graphRow struct {
	id        string
	changes   int
	createdAt time.Time
}

type diffRow struct {
	id        string
	from, to  int
	material  bool
	fields    []string
	changes   int
	createdAt time.Time
}

// attribution is the E7 record kept on the build_context step (knowledge_attribution.v1.json).
type attribution struct {
	AsOf             *time.Time `json:"as_of"`
	Retrieved        []string   `json:"retrieved"`
	Applicable       []string   `json:"applicable"`
	ExceptionBlocked []string   `json:"exception_blocked"`
	Used             []string   `json:"used"`
	// ClosestLesson is the plain statement of the run's closest-match record (detail.knowledge_retrieval.message,
	// ADR-0013 amendment 2); "" for a run recorded before it existed. Not part of knowledge_attribution.
	ClosestLesson string `json:"-"`
}

// guidanceEntry is one knowledge object the DecisionGuidance considered.
type guidanceEntry struct {
	KnowledgeID string `json:"knowledge_id"`
	Applies     bool   `json:"applies"`
	Exceptions  []struct {
		Triggered bool `json:"triggered"`
	} `json:"exceptions_checked"`
	Evidence []json.RawMessage `json:"current_evidence_refs"`
}

type surfaceRow struct {
	subjectID, surface, kind, channel string
	ts                                *string
	reservedAt                        time.Time
}

type inferenceRow struct {
	id, verdict string
}

// stateDiffID is the diff the episode was decided on: the one it stores, else the one its account change names.
func (in *traceInput) stateDiffID() *string {
	if in.episode.StateDiffID != nil {
		return in.episode.StateDiffID
	}
	if in.change != nil {
		return &in.change.stateDiffID
	}
	return nil
}

func loadTraceInput(ctx context.Context, db claimstore.DB, episodeID string) (*traceInput, error) {
	e, err := loadEpisodeRow(ctx, db, episodeID)
	if err != nil {
		return nil, err
	}
	in := &traceInput{episode: e}
	steps := []func() error{
		func() (err error) { in.triggers, err = loadTriggers(ctx, db, e.RunID); return },
		func() (err error) { in.change, err = loadChange(ctx, db, e.AccountChangeID); return },
		func() (err error) { in.diff, err = loadDiff(ctx, db, in.stateDiffID()); return },
		func() (err error) { in.attr, err = loadAttribution(ctx, db, e.RunID); return },
		func() (err error) { in.guidance, err = loadGuidance(ctx, db, e.GuidanceID); return },
		func() (err error) { in.cands, err = loadCandidates(ctx, db, e.ID); return },
		func() (err error) { in.setAt, err = loadSetTime(ctx, db, e.ID); return },
		func() (err error) { in.surfaces, err = loadSurfaces(ctx, db, e); return },
		func() (err error) { in.decision, err = loadStrategyDecision(ctx, db, e.ID); return },
		func() (err error) { in.inference, err = loadInference(ctx, db, e.ID); return },
		func() (err error) { in.reactions, err = loadReactionIDs(ctx, db, e.ID); return },
		func() (err error) { in.mutations, err = buildMutations(ctx, db, e.ID); return },
		func() (err error) {
			rs, err := loadResults(ctx, db, []string{e.RunID})
			in.results = rs[e.RunID]
			return err
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	in.graph, err = loadGraph(ctx, db, in.triggers)
	return in, err
}

func loadTriggers(ctx context.Context, db claimstore.DB, runID string) ([]triggerRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.id::text, a.source_event_id::text, a.activity_type, a.source_system, a.occurred_at, a.summary,
 a.account_hint, a.account_id::text,
 (SELECT count(*) FROM activity_participants p WHERE p.activity_id = a.id),
 (SELECT count(*) FROM activity_participants p WHERE p.activity_id = a.id AND p.person_id IS NOT NULL),
 (SELECT count(*) FROM entity_source_mappings m WHERE m.evidence_activity_id = a.id)
 FROM agent_runs ar JOIN activities a ON a.id = ANY(ar.trigger_activity_ids)
 WHERE ar.id = $1::uuid ORDER BY a.occurred_at, a.id`, runID)
	if err != nil {
		return nil, fmt.Errorf("controlplane: read trigger activities: %w", err)
	}
	defer rows.Close()
	var out []triggerRow
	for rows.Next() {
		var t triggerRow
		if err := rows.Scan(&t.id, &t.sourceEventID, &t.activityType, &t.sourceSystem, &t.occurredAt, &t.summary, &t.accountHint,
			&t.accountID, &t.participants, &t.resolved, &t.mappings); err != nil {
			return nil, fmt.Errorf("controlplane: scan trigger activity: %w", err)
		}
		t.occurredAt = t.occurredAt.UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

func loadChange(ctx context.Context, db claimstore.DB, id *string) (*changeRow, error) {
	if id == nil {
		return nil, nil
	}
	var c changeRow
	var refs []byte
	err := db.QueryRowContext(ctx, `SELECT id::text, state_diff_id::text, evidence_refs, created_at, material_change FROM account_changes WHERE id = $1::uuid`, *id).
		Scan(&c.id, &c.stateDiffID, &refs, &c.createdAt, &c.material)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: read account change: %w", err)
	}
	c.evidenceRefs, c.createdAt = refs, c.createdAt.UTC()
	return &c, nil
}

func loadDiff(ctx context.Context, db claimstore.DB, id *string) (*diffRow, error) {
	if id == nil {
		return nil, nil
	}
	var d diffRow
	var changes []byte
	err := db.QueryRowContext(ctx, `SELECT id::text, from_version, to_version, is_material, changes, created_at FROM state_diffs WHERE id = $1::uuid`, *id).
		Scan(&d.id, &d.from, &d.to, &d.material, &changes, &d.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: read state diff: %w", err)
	}
	var list []struct {
		Field string `json:"field"`
	}
	if err := json.Unmarshal(changes, &list); err != nil {
		return nil, fmt.Errorf("controlplane: decode state diff changes: %w", err)
	}
	d.changes, d.createdAt = len(list), d.createdAt.UTC()
	for _, c := range list {
		if c.Field != "" && len(d.fields) < maxListed {
			d.fields = append(d.fields, c.Field)
		}
	}
	return &d, nil
}

// maxListed bounds the ids and field names a span lists in its attributes.
const maxListed = 20

func loadAttribution(ctx context.Context, db claimstore.DB, runID string) (*attribution, error) {
	var detail []byte
	err := db.QueryRowContext(ctx, `SELECT detail -> 'knowledge_attribution' FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'build_context'`, runID).Scan(&detail)
	if errors.Is(err, sql.ErrNoRows) || len(detail) == 0 || string(detail) == "null" {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: read knowledge attribution: %w", err)
	}
	var a attribution
	if err := json.Unmarshal(detail, &a); err != nil {
		return nil, fmt.Errorf("controlplane: decode knowledge attribution: %w", err)
	}
	var msg sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT detail -> 'knowledge_retrieval' ->> 'message' FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'build_context'`, runID).Scan(&msg); err != nil {
		return nil, fmt.Errorf("controlplane: read the closest lesson: %w", err)
	}
	a.ClosestLesson = msg.String
	return &a, nil
}

func loadGuidance(ctx context.Context, db claimstore.DB, id *string) ([]guidanceEntry, error) {
	if id == nil {
		return nil, nil
	}
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT guidance -> 'supporting_knowledge' FROM decision_guidance WHERE id = $1::uuid`, *id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: read decision guidance: %w", err)
	}
	var entries []guidanceEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("controlplane: decode decision guidance: %w", err)
	}
	return entries, nil
}

func loadSetTime(ctx context.Context, db claimstore.DB, episodeID string) (*time.Time, error) {
	var t time.Time
	err := db.QueryRowContext(ctx, `SELECT generated_at FROM strategy_sets WHERE decision_episode_id = $1::uuid`, episodeID).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: read strategy set time: %w", err)
	}
	return at(t), nil
}

// loadSurfaces reads the Cliff message refs of the episode: Message 1 is keyed by the business-intelligence update,
// Messages 2 and 3 by the episode.
func loadSurfaces(ctx context.Context, db claimstore.DB, e episodeRow) ([]surfaceRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT subject_id::text, surface, kind, channel, ts, reserved_at FROM surface_messages
 WHERE (kind = 'bi' AND subject_id = $2::uuid) OR (kind IN ('chooser', 'judgment') AND subject_id = $1::uuid)
 ORDER BY reserved_at, surface, kind`, e.ID, e.BIUpdateID)
	if err != nil {
		return nil, fmt.Errorf("controlplane: read surface messages: %w", err)
	}
	defer rows.Close()
	var out []surfaceRow
	for rows.Next() {
		var s surfaceRow
		if err := rows.Scan(&s.subjectID, &s.surface, &s.kind, &s.channel, &s.ts, &s.reservedAt); err != nil {
			return nil, fmt.Errorf("controlplane: scan surface message: %w", err)
		}
		s.reservedAt = s.reservedAt.UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

func loadInference(ctx context.Context, db claimstore.DB, episodeID string) (*inferenceRow, error) {
	var i inferenceRow
	err := db.QueryRowContext(ctx, `SELECT id::text, human_verdict FROM judgment_inferences WHERE decision_episode_id = $1::uuid`, episodeID).Scan(&i.id, &i.verdict)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: read judgment inference: %w", err)
	}
	return &i, nil
}

func loadReactionIDs(ctx context.Context, db claimstore.DB, episodeID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text FROM customer_reactions WHERE decision_episode_id = $1::uuid ORDER BY created_at, id LIMIT $2`, episodeID, maxListed)
	if err != nil {
		return nil, fmt.Errorf("controlplane: read customer reactions: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("controlplane: scan customer reaction: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// loadGraph is the projection diff naming the trigger event (the newest when the event was projected more than once).
func loadGraph(ctx context.Context, db claimstore.DB, triggers []triggerRow) (*graphRow, error) {
	if len(triggers) == 0 {
		return nil, nil
	}
	var events []string
	for _, t := range triggers {
		events = append(events, t.sourceEventID)
	}
	var g graphRow
	err := db.QueryRowContext(ctx, `SELECT id::text, jsonb_array_length(changes), created_at FROM graph_projection_diffs
 WHERE source_event_ids && string_to_array($1, ',')::uuid[] ORDER BY id DESC LIMIT 1`, strings.Join(events, ",")).Scan(&g.id, &g.changes, &g.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: read graph diff: %w", err)
	}
	g.createdAt = g.createdAt.UTC()
	return &g, nil
}
