package evalreport

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
)

// world is everything a report reads, loaded once per Generate call.
type world struct {
	versions   map[string]*versionRow       // by tag
	ordered    []versionKey                 // evaluator_versions ∪ observed eval_runs tags, sorted
	episodes   map[string]*episodeRow       // decided episodes by id
	byRun      map[string]*episodeRow       // decided episodes by agent_run_id
	anyByRun   map[string]*episodeRow       // every episode (incl. awaiting/chosen) by agent_run_id
	finalEvals []*evalRow                   // eval_runs on decided episodes' final drafts
	allEvals   []*evalRow                   // every eval_runs row (repeat-judgment grouping, axis fail windows)
	deltas     map[string]*deltaRow         // human_deltas by id
	explains   map[string][]explRow         // delta id -> explaining eval rows
	candidates map[string]map[int]draftJSON // run -> draft_index -> strategy candidate payload
	decisions  map[string]*hsdRow           // episode id -> human_strategy_decisions row
	retired    map[string]time.Time         // version tag -> earliest eval_promotions retirement
	inferences map[string]*inferenceRow     // episode id -> judgment_inferences row + verdict history
}

// versionRow is one evaluator_versions row.
type versionRow struct {
	Evaluator     string
	Version       int
	Status        string
	Kind          string
	CreatedFrom   string
	SourceDeltaID string
	AccountID     string
	KnowledgeID   string
	PromotedAt    *time.Time
	Spec          learning.Spec
	SpecErr       string // non-empty when shadow_spec is present but cannot be decoded
}

// episodeRow is one decision_episode row; decided ones (status 'decided'/'judged') carry the human
// action and the linked human_decisions.created_at.
type episodeRow struct {
	ID, RunID, AccountID string
	Status               string
	FinalDraftIndex      int // -1 until decided
	HumanAction          string
	HumanDeltaID         string
	Reason               string    // human_decisions.reason: why the human rejected (empty when none was stated)
	DecidedAt            time.Time // human_decisions.created_at: send-time eval rows share this clock
	HasDecision          bool      // a human_decisions row is linked (always true when decided)
	CreatedAt            time.Time
}

// evalRow is one eval_runs row.
type evalRow struct {
	ID, RunID  string
	DraftIndex int
	Evaluator  string
	Tag        string // evaluator_version ('<evaluator>:v<N>')
	Kind       string
	Verdict    string
	Score      *float64
	Confidence *float64
	CreatedAt  time.Time
}

// explRow is one human_delta_explanations link resolved to its eval_runs row.
type explRow struct {
	EvalRunID string
	Evaluator string
	Tag       string
	Verdict   string
}

// deltaRow is one human_deltas row.
type deltaRow struct {
	ID, EpisodeID string
	Changes       []learning.LiteralChange
	Labels        []string
	Unexplained   bool
	Suggested     string // candidate_criterion.suggested_eval_type
	CreatedAt     time.Time
	SeededTag     string // '<evaluator>:v<N>' of the evaluator_version this delta seeded ("" none)
}

// draftJSON carries the stored payloads a judged draft is reconstructed from.
type draftJSON struct {
	ActionType       string
	To, CC, Artifact []byte
}

// hsdRow is the human_strategy_decisions row of an episode: the stored final draft (NULL until the
// human edits), the selected candidate's draft index and the send clock.
type hsdRow struct {
	EpisodeID     string
	SendDecision  string
	SendDecidedAt *time.Time
	SelectedDraft int // draft_index of the selected candidate (-1 unknown)
	Final         draftJSON
}

// loadSnapshot reads the world in one read-only REPEATABLE READ transaction when db can open one.
func loadSnapshot(ctx context.Context, db claimstore.DB) (*world, error) {
	b, ok := db.(txBeginner)
	if !ok {
		return load(ctx, db)
	}
	tx, err := b.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("evalreport: begin snapshot transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // read-only: nothing to commit
	return load(ctx, tx)
}

func load(ctx context.Context, db claimstore.DB) (*world, error) {
	w := &world{
		versions:   map[string]*versionRow{},
		episodes:   map[string]*episodeRow{},
		byRun:      map[string]*episodeRow{},
		anyByRun:   map[string]*episodeRow{},
		deltas:     map[string]*deltaRow{},
		explains:   map[string][]explRow{},
		candidates: map[string]map[int]draftJSON{},
		decisions:  map[string]*hsdRow{},
		retired:    map[string]time.Time{},
		inferences: map[string]*inferenceRow{},
	}
	for i, l := range []func(context.Context, claimstore.DB) error{
		w.loadVersions, w.loadTags, w.loadEpisodes, w.loadFinalEvals, w.loadAllEvals,
		w.loadDeltas, w.loadExplains, w.loadCandidates, w.loadDecisions, w.loadRetirements, w.loadInferences,
	} {
		if err := l(ctx, db); err != nil {
			return nil, err
		}
		if betweenReads != nil {
			betweenReads(i + 1)
		}
	}
	w.orderKeys()
	return w, nil
}

func (w *world) loadVersions(ctx context.Context, db claimstore.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT evaluator::text, version, status, kind, created_from,
 COALESCE(source_human_delta_id::text, ''), COALESCE(account_id::text, ''),
 COALESCE(knowledge_id::text, ''), promoted_at, COALESCE(shadow_spec::text, '')
FROM evaluator_versions ORDER BY evaluator, version`)
	if err != nil {
		return fmt.Errorf("evalreport: load versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v versionRow
		var promoted sql.NullTime
		var specRaw string
		if err := rows.Scan(&v.Evaluator, &v.Version, &v.Status, &v.Kind, &v.CreatedFrom,
			&v.SourceDeltaID, &v.AccountID, &v.KnowledgeID, &promoted, &specRaw); err != nil {
			return fmt.Errorf("evalreport: scan version: %w", err)
		}
		if promoted.Valid {
			t := promoted.Time.UTC()
			v.PromotedAt = &t
		}
		spec, err := parseSpec(specRaw)
		if err != nil {
			v.SpecErr = err.Error() // carried on the version: only its spec-dependent metrics degrade
		}
		v.Spec = spec
		k := keyOf(v.Evaluator, v.Version)
		w.versions[k.tag] = &v
	}
	return rows.Err()
}

// loadTags adds the eval_runs version tags that have no evaluator_versions row (the shipped v1s).
func (w *world) loadTags(ctx context.Context, db claimstore.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT evaluator::text, evaluator_version FROM eval_runs`)
	if err != nil {
		return fmt.Errorf("evalreport: load observed tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var evaluator, tag string
		if err := rows.Scan(&evaluator, &tag); err != nil {
			return fmt.Errorf("evalreport: scan tag: %w", err)
		}
		v, ok := parseTag(tag)
		if !ok || v.evaluator != evaluator {
			continue // a tag that does not parse as '<evaluator>:v<N>' reports under its own row only
		}
		if _, reg := w.versions[tag]; !reg {
			w.versions[tag] = &versionRow{Evaluator: evaluator, Version: v.version, Status: "observed"}
		}
	}
	return rows.Err()
}

// loadEpisodes loads every episode (a pending or refused send's episode still anchors eval rows —
// the send-time results of a refused send persist while the episode stays 'chosen'). Only decided
// episodes land in episodes/byRun.
func (w *world) loadEpisodes(ctx context.Context, db claimstore.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT de.id::text, de.agent_run_id::text, de.account_id::text,
 de.status, COALESCE(de.final_draft_index, -1), COALESCE(de.human_action, ''),
 COALESCE(de.human_delta_id::text, ''), hd.created_at, de.created_at, COALESCE(hd.reason, '')
FROM decision_episodes de LEFT JOIN human_decisions hd ON hd.id = de.human_decision_id
ORDER BY de.created_at, de.id`)
	if err != nil {
		return fmt.Errorf("evalreport: load episodes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e episodeRow
		var decided sql.NullTime
		if err := rows.Scan(&e.ID, &e.RunID, &e.AccountID, &e.Status, &e.FinalDraftIndex, &e.HumanAction,
			&e.HumanDeltaID, &decided, &e.CreatedAt, &e.Reason); err != nil {
			return fmt.Errorf("evalreport: scan episode: %w", err)
		}
		e.HasDecision = decided.Valid
		if decided.Valid {
			e.DecidedAt = decided.Time.UTC()
		}
		e.CreatedAt = e.CreatedAt.UTC()
		if e.Status == "decided" || e.Status == "judged" {
			w.episodes[e.ID] = &e
			w.byRun[e.RunID] = &e
		}
		w.anyByRun[e.RunID] = &e
	}
	return rows.Err()
}

// scanEvals reads eval_runs rows of a query into evalRow.
func (w *world) scanEvals(rows *sql.Rows) ([]*evalRow, error) {
	defer rows.Close()
	var out []*evalRow
	for rows.Next() {
		var r evalRow
		var score, conf sql.NullFloat64
		if err := rows.Scan(&r.ID, &r.RunID, &r.DraftIndex, &r.Evaluator, &r.Tag, &r.Kind,
			&r.Verdict, &score, &conf, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("evalreport: scan eval run: %w", err)
		}
		r.CreatedAt = r.CreatedAt.UTC()
		if score.Valid {
			s := score.Float64
			r.Score = &s
		}
		if conf.Valid {
			c := conf.Float64
			r.Confidence = &c
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

const evalCols = `er.id::text, er.agent_run_id::text, er.draft_index, er.evaluator::text,
 er.evaluator_version, er.kind, er.verdict, er.score, er.confidence, er.created_at`

// loadFinalEvals reads the eval rows on decided episodes' final drafts — the agreement basis.
func (w *world) loadFinalEvals(ctx context.Context, db claimstore.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT `+evalCols+`
FROM eval_runs er JOIN decision_episodes de
  ON de.agent_run_id = er.agent_run_id AND de.final_draft_index = er.draft_index
WHERE de.status IN ('decided','judged')
ORDER BY er.agent_run_id, er.evaluator, er.evaluator_version, er.kind, er.created_at, er.id`)
	if err != nil {
		return fmt.Errorf("evalreport: load final evals: %w", err)
	}
	w.finalEvals, err = w.scanEvals(rows)
	return err
}

// loadAllEvals reads every eval_runs row — the repeat-judgment grouping and the activation windows.
func (w *world) loadAllEvals(ctx context.Context, db claimstore.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT `+evalCols+` FROM eval_runs er
ORDER BY er.agent_run_id, er.evaluator, er.evaluator_version, er.kind, er.created_at, er.id`)
	if err != nil {
		return fmt.Errorf("evalreport: load eval runs: %w", err)
	}
	w.allEvals, err = w.scanEvals(rows)
	return err
}
