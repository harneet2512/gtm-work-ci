package abcrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
)

// LearningVersion is written into the frozen previous-deal learning evidence and refused when it differs.
const LearningVersion = "uplift_learning.v1"

// EvidenceLine is one unit of lifecycle evidence of a learned item (bench/uplift/learn.py).
type EvidenceLine struct {
	Kind        string    `json:"kind"`
	RefID       string    `json:"ref_id"`
	Polarity    string    `json:"polarity,omitempty"`
	OutcomeType string    `json:"outcome_type,omitempty"`
	At          time.Time `json:"at"`
}

// LearnedItem is one knowledge candidate the previous-deal learner promoted, with the evidence it earned.
type LearnedItem struct {
	ID                     string                `json:"id"`
	Key                    string                `json:"key"`
	DecisionPoint          string                `json:"decision_point"`
	Title                  string                `json:"title"`
	SituationSignature     []knowledge.Condition `json:"situation_signature"`
	Guidance               knowledge.Guidance    `json:"guidance"`
	CreatedAt              time.Time             `json:"created_at"`
	SourceDecisionEpisode  string                `json:"source_decision_episode_id"`
	Evidence               []EvidenceLine        `json:"evidence"`
	PreviousEpisodeSupport []string              `json:"supporting_episode_ids"`
}

// Learning is the frozen learning evidence file.
type Learning struct {
	Version            string        `json:"version"`
	Cutoff             time.Time     `json:"cutoff"`
	Knowledge          []LearnedItem `json:"knowledge"`
	PreviousEpisodeIDs []string      `json:"previous_episode_ids"`
}

// LoadLearning reads the learning evidence.
func LoadLearning(path string) (Learning, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Learning{}, fmt.Errorf("abcrun: read learning evidence: %w", err)
	}
	var l Learning
	if err := json.Unmarshal(raw, &l); err != nil {
		return Learning{}, fmt.Errorf("abcrun: decode learning evidence %s: %w", path, err)
	}
	if l.Version != LearningVersion {
		return Learning{}, fmt.Errorf("abcrun: learning evidence version %q, want %q", l.Version, LearningVersion)
	}
	return l, nil
}

// knowledgeOf is the candidate the lifecycle starts from: no status, no counts, no evidence (Insert refuses anything
// unearned); everything it becomes it earns from the recorded evidence.
func (it LearnedItem) knowledgeOf() knowledge.Knowledge {
	key, src := it.Key, it.SourceDecisionEpisode
	return knowledge.Knowledge{ID: it.ID, Key: &key, Title: it.Title, SituationSignature: it.SituationSignature, Guidance: it.Guidance,
		Status: knowledge.StatusCandidate, Exceptions: []knowledge.Exception{},
		Provenance: knowledge.Provenance{CreatedFrom: "human_delta", SourceDecisionEpisodeID: &src},
		CreatedAt:  it.CreatedAt.UTC()}
}

func (e EvidenceLine) evidence() knowledge.Evidence {
	return knowledge.Evidence{Kind: e.Kind, RefID: e.RefID, Polarity: e.Polarity, OutcomeType: e.OutcomeType, At: e.At.UTC()}
}

// InstallStore learns the items into an EMPTY store through the real lifecycle: each item is inserted as an
// unsupported candidate (knowledgestore.InsertReplay) and earns its status only from its evidence
// (knowledgestore.RecordEvidence, oldest first). It refuses a store that is not empty, so nothing seeded or manual can
// be mixed in. Items already installed are not touched.
func InstallStore(ctx context.Context, db *sql.DB, rules knowledge.Rules, items []LearnedItem) error {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM knowledge`).Scan(&n); err != nil {
		return fmt.Errorf("abcrun: count knowledge: %w", err)
	}
	if n != 0 {
		return fmt.Errorf("abcrun: the knowledge store holds %d items before learning: the experiment store must start empty", n)
	}
	return install(ctx, db, rules, items)
}

func install(ctx context.Context, db *sql.DB, rules knowledge.Rules, items []LearnedItem) error {
	for _, it := range items {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := knowledgestore.InsertReplay(ctx, tx, it.knowledgeOf(), "learned from previous deals"); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("abcrun: insert %s: %w", it.Key, err)
		}
		lines := slices.Clone(it.Evidence)
		slices.SortStableFunc(lines, func(a, b EvidenceLine) int { return a.At.Compare(b.At) })
		for _, e := range lines {
			if _, _, err := knowledgestore.RecordEvidence(ctx, tx, it.ID, e.evidence(), rules); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("abcrun: evidence %s %s of %s: %w", e.Kind, e.RefID, it.Key, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceStore swaps the knowledge a run will read: it empties the store (the append-only history is purged under the
// experiment's own purge switch) and learns the given subset again through the same lifecycle path. It exists only
// so each arm of a situation reads exactly its own knowledge.
func ReplaceStore(ctx context.Context, db *sql.DB, rules knowledge.Rules, items []LearnedItem) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range []string{`SET LOCAL ghost.purge_knowledge = 'on'`, `DELETE FROM knowledge_status_history`, `DELETE FROM knowledge`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("abcrun: empty the knowledge store: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return install(ctx, db, rules, items)
}

// ExportStore returns every stored knowledge object as the lifecycle left it.
func ExportStore(ctx context.Context, db *sql.DB) ([]knowledge.Knowledge, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text FROM knowledge ORDER BY key`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]knowledge.Knowledge, 0, len(ids))
	for _, id := range ids {
		k, err := knowledgestore.Get(ctx, db, id)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}
