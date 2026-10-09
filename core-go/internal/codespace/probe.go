package codespace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

// FormedKnowledge is one knowledge object of a case's database.
type FormedKnowledge struct {
	ID        string
	Status    string
	CreatedAt time.Time
	// NotOffered says why the knowledge would not be offered to a later episode (knowledge.Rules.ApplicableKnowledge); empty when
	// it would be, or when the probe has no rules to say.
	NotOffered string
}

// KnowledgeProbe reads what the learning loop did, straight from a case's database.
type KnowledgeProbe interface {
	// Formed lists the case's knowledge.
	Formed(ctx context.Context, c Case) ([]FormedKnowledge, error)
	// Retrieved lists the knowledge ids a run's context build retrieved (E7: retrieved, applicable, used).
	Retrieved(ctx context.Context, c Case, runID string) ([]string, error)
	// Used lists the knowledge ids the run's candidates cite (E7 knowledge_attribution.used, filled when the run publishes).
	Used(ctx context.Context, c Case, runID string) ([]string, error)
	// Applicable lists the knowledge ids the run judged applicable: what was offered to its agent.
	Applicable(ctx context.Context, c Case, runID string) ([]string, error)
	// Retrieval is the run's persisted closest-match record (build_context.detail.knowledge_retrieval, ADR-0013 amendment 2):
	// the lessons ranked by similarity with their scores and decisions. An error when the run persisted none.
	Retrieval(ctx context.Context, c Case, runID string) (knowledge.Retrieval, error)
	// Closeness computes, read-only, how close the given lessons are to the case's account state as it stands now. Before
	// the case is played that is the state before Event N, so it is an estimate: Event N's own claims are not ingested yet.
	Closeness(ctx context.Context, c Case, lessons []FormedKnowledge) (knowledge.Retrieval, error)
	// ReplayClock is the world time of the run's Event N: the time the run's knowledge was read at.
	ReplayClock(ctx context.Context, c Case, runID string) (time.Time, error)
}

// DBProbe is KnowledgeProbe over the case databases.
type DBProbe struct {
	DSNFor func(Case) (string, error)
	// RulesPath is the knowledge lifecycle rules file; with it Formed also says which entries would not be offered.
	RulesPath string
}

func (p DBProbe) open(ctx context.Context, c Case) (*sql.DB, error) {
	dsn, err := p.DSNFor(c)
	if err != nil {
		return nil, err
	}
	return store.Open(ctx, dsn)
}

// Formed implements KnowledgeProbe.
func (p DBProbe) Formed(ctx context.Context, c Case) ([]FormedKnowledge, error) {
	db, err := p.open(ctx, c)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id::text, status, created_at FROM knowledge ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list the knowledge of %s: %w", c.Label, err)
	}
	defer rows.Close()
	var out []FormedKnowledge
	for rows.Next() {
		var k FormedKnowledge
		if err := rows.Scan(&k.ID, &k.Status, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.CreatedAt = k.CreatedAt.UTC()
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, p.markNotOffered(ctx, db, out)
}

// markNotOffered fills NotOffered of every entry the lifecycle rules would not offer to a later episode.
func (p DBProbe) markNotOffered(ctx context.Context, db *sql.DB, out []FormedKnowledge) error {
	if p.RulesPath == "" {
		return nil
	}
	rules, err := knowledge.LoadRules(p.RulesPath)
	if err != nil {
		return fmt.Errorf("load the knowledge lifecycle rules: %w", err)
	}
	for i := range out {
		k, err := knowledgestore.Get(ctx, db, out[i].ID)
		if err != nil {
			return fmt.Errorf("read knowledge %s: %w", out[i].ID, err)
		}
		if !rules.ApplicableKnowledge(k) {
			out[i].NotOffered = fmt.Sprintf("status %s, created_from %q, %d supporting decisions, scope fields %v",
				k.Status, k.Provenance.CreatedFrom, k.Counts.Decisions, knowledge.ScopeFields(k))
		}
	}
	return nil
}

// attribution reads one list of the run's knowledge_attribution record on its build_context step.
func (p DBProbe) attribution(ctx context.Context, c Case, runID, field string) ([]string, error) {
	db, err := p.open(ctx, c)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var raw []byte
	err = db.QueryRowContext(ctx, `SELECT detail->'knowledge_attribution'->$2::text FROM agent_run_steps
	  WHERE agent_run_id = $1::uuid AND step = 'build_context'`, runID, field).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("run %s has no build_context step to read its %s knowledge from", runID, field)
	}
	if err != nil {
		return nil, fmt.Errorf("read the %s knowledge of run %s: %w", field, runID, err)
	}
	var ids []string
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &ids); err != nil {
			return nil, fmt.Errorf("the %s knowledge of run %s is not a list of ids: %w", field, runID, err)
		}
	}
	return ids, nil
}

// Retrieved implements KnowledgeProbe.
func (p DBProbe) Retrieved(ctx context.Context, c Case, runID string) ([]string, error) {
	return p.attribution(ctx, c, runID, "retrieved")
}

// Used implements KnowledgeProbe.
func (p DBProbe) Used(ctx context.Context, c Case, runID string) ([]string, error) {
	return p.attribution(ctx, c, runID, "used")
}

// Applicable implements KnowledgeProbe.
func (p DBProbe) Applicable(ctx context.Context, c Case, runID string) ([]string, error) {
	return p.attribution(ctx, c, runID, "applicable")
}

// Retrieval implements KnowledgeProbe.
func (p DBProbe) Retrieval(ctx context.Context, c Case, runID string) (knowledge.Retrieval, error) {
	db, err := p.open(ctx, c)
	if err != nil {
		return knowledge.Retrieval{}, err
	}
	defer db.Close()
	var raw []byte
	err = db.QueryRowContext(ctx, `SELECT detail->'knowledge_retrieval' FROM agent_run_steps
	  WHERE agent_run_id = $1::uuid AND step = 'build_context'`, runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.Retrieval{}, fmt.Errorf("run %s has no build_context step to read its closest-match record from", runID)
	}
	if err != nil {
		return knowledge.Retrieval{}, fmt.Errorf("read the closest-match record of run %s: %w", runID, err)
	}
	var rv knowledge.Retrieval
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &rv) != nil || rv.Message == "" {
		return knowledge.Retrieval{}, fmt.Errorf("run %s persisted no closest-match record (build_context.detail.knowledge_retrieval)", runID)
	}
	return rv, nil
}

// farFuture reads an account's latest state: as of a time after every replayed event.
var farFuture = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

// Closeness implements KnowledgeProbe.
func (p DBProbe) Closeness(ctx context.Context, c Case, lessons []FormedKnowledge) (knowledge.Retrieval, error) {
	if p.RulesPath == "" {
		return knowledge.Retrieval{}, errors.New("no lifecycle rules path: the similarity rules are not known")
	}
	rules, err := knowledge.LoadRules(p.RulesPath)
	if err != nil {
		return knowledge.Retrieval{}, err
	}
	if rules.Similarity == nil {
		return knowledge.Retrieval{}, errors.New("no similarity rules beside the lifecycle rules")
	}
	db, err := p.open(ctx, c)
	if err != nil {
		return knowledge.Retrieval{}, err
	}
	defer db.Close()
	var accountID string
	if err := db.QueryRowContext(ctx, `SELECT account_id::text FROM account_state ORDER BY computed_at DESC LIMIT 1`).Scan(&accountID); err != nil {
		return knowledge.Retrieval{}, fmt.Errorf("find the account of %s: %w", c.Label, err)
	}
	sit, found, err := signalstore.SituationAt(ctx, db, accountID, farFuture, coalesce.WorldAsOf)
	if err != nil || !found {
		return knowledge.Retrieval{}, fmt.Errorf("read the state of %s (found %v): %v", c.Label, found, err)
	}
	var ks []knowledge.Knowledge
	for _, l := range lessons {
		k, err := knowledgestore.Get(ctx, db, l.ID)
		if err != nil {
			return knowledge.Retrieval{}, fmt.Errorf("read knowledge %s: %w", l.ID, err)
		}
		ks = append(ks, k)
	}
	_, rv, err := knowledge.RetrieveAll(ks, sit, rules.Similarity)
	if err != nil || rv == nil {
		return knowledge.Retrieval{}, fmt.Errorf("score the lessons against %s: %v", c.Label, err)
	}
	return *rv, nil
}

// ReplayClock implements KnowledgeProbe: the build_context step's replay_clock, which core stamps from the event's own time.
func (p DBProbe) ReplayClock(ctx context.Context, c Case, runID string) (time.Time, error) {
	db, err := p.open(ctx, c)
	if err != nil {
		return time.Time{}, err
	}
	defer db.Close()
	var clock sql.NullString
	err = db.QueryRowContext(ctx, `SELECT detail->>'replay_clock' FROM agent_run_steps
	  WHERE agent_run_id = $1::uuid AND step = 'build_context'`, runID).Scan(&clock)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, fmt.Errorf("run %s has no build_context step to read its replay clock from", runID)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read the replay clock of run %s: %w", runID, err)
	}
	t, perr := time.Parse(time.RFC3339Nano, clock.String)
	if !clock.Valid || perr != nil {
		return time.Time{}, fmt.Errorf("run %s has no readable replay clock (%q)", runID, clock.String)
	}
	return t.UTC(), nil
}

var _ KnowledgeProbe = DBProbe{}
