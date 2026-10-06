package codespace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

// FormedKnowledge is one knowledge object of a case's database.
type FormedKnowledge struct {
	ID        string
	Status    string
	CreatedAt time.Time
}

// KnowledgeProbe reads what the learning loop did, straight from a case's database.
type KnowledgeProbe interface {
	// Formed lists the case's knowledge.
	Formed(ctx context.Context, c Case) ([]FormedKnowledge, error)
	// Retrieved lists the knowledge ids a run's context build retrieved (E7: retrieved, applicable, used).
	Retrieved(ctx context.Context, c Case, runID string) ([]string, error)
	// Used lists the knowledge ids the run's candidates cite (E7 knowledge_attribution.used, filled when the run publishes).
	Used(ctx context.Context, c Case, runID string) ([]string, error)
	// ReplayClock is the world time of the run's Event N: the time the run's knowledge was read at.
	ReplayClock(ctx context.Context, c Case, runID string) (time.Time, error)
}

// DBProbe is KnowledgeProbe over the case databases.
type DBProbe struct {
	DSNFor func(Case) (string, error)
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
	return out, rows.Err()
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
