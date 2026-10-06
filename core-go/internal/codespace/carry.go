package codespace

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

// KnowledgeCarrier moves what the earlier case learned into the later case's database. Company knowledge is
// deployment-wide but every case has its own database (a freeze needs an empty one), so each entry arrives as a fresh
// candidate stamped with its OWN replay time in the earlier case (never the latest entry's, never the wall clock) and is then given its evidence through the real lifecycle
// (knowledgestore.RecordEvidence under the same rules): its status is earned again, never copied, and its
// created_at stays before the later episode, so the as-of read of the later case can see it. This follows the proof
// runner's carry step (internal/proofrun/carry.go on har129-live-proof). It is idempotent: entries already present are skipped.
type KnowledgeCarrier struct {
	// DSNFor is the connection string of a case's database.
	DSNFor func(Case) (string, error)
	// RulesPath is the knowledge lifecycle rules file.
	RulesPath string
}

type carried struct {
	K        knowledge.Knowledge
	Evidence []knowledge.Evidence
	// At is when the entry was learned in the earlier case's replay time; it stays the entry's created_at.
	At time.Time
}

// Carry implements Ops.Carry.
func (k KnowledgeCarrier) Carry(ctx context.Context, from, to Case) (int, error) {
	rules, err := knowledge.LoadRules(k.RulesPath)
	if err != nil {
		return 0, fmt.Errorf("load the knowledge lifecycle rules: %w", err)
	}
	src, err := k.open(ctx, from)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	dst, err := k.open(ctx, to)
	if err != nil {
		return 0, err
	}
	defer dst.Close()
	stamps, err := knowledgeStamps(ctx, src)
	if err != nil || len(stamps) == 0 {
		return 0, err
	}
	ids := make([]string, 0, len(stamps))
	for _, st := range stamps {
		ids = append(ids, st.ID)
	}
	fresh, err := missingIDs(ctx, dst, ids)
	if err != nil || len(fresh) == 0 {
		return 0, err
	}
	items, err := readCarried(ctx, src, fresh)
	if err != nil {
		return 0, err
	}
	at := map[string]time.Time{}
	for _, st := range stamps {
		at[st.ID] = st.At
	}
	for i := range items {
		items[i].At = at[items[i].K.ID]
	}
	return seedCarried(ctx, dst, rules, items)
}

func (k KnowledgeCarrier) open(ctx context.Context, c Case) (*sql.DB, error) {
	dsn, err := k.DSNFor(c)
	if err != nil {
		return nil, err
	}
	return store.Open(ctx, dsn)
}

type stamp struct {
	ID string
	At time.Time
}

// knowledgeStamps lists the knowledge of a database with each entry's own created_at (its replay time).
func knowledgeStamps(ctx context.Context, db *sql.DB) ([]stamp, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text, created_at FROM knowledge ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list the earlier case's knowledge: %w", err)
	}
	defer rows.Close()
	var out []stamp
	for rows.Next() {
		var st stamp
		if err := rows.Scan(&st.ID, &st.At); err != nil {
			return nil, err
		}
		st.At = st.At.UTC()
		out = append(out, st)
	}
	return out, rows.Err()
}

func missingIDs(ctx context.Context, db *sql.DB, ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM knowledge WHERE id = $1::uuid`, id).Scan(&n); err != nil {
			return nil, fmt.Errorf("look for %s in the later case: %w", id, err)
		}
		if n == 0 {
			out = append(out, id)
		}
	}
	return out, nil
}

func readCarried(ctx context.Context, db *sql.DB, ids []string) ([]carried, error) {
	var out []carried
	for _, id := range ids {
		kn, err := knowledgestore.Get(ctx, db, id)
		if err != nil {
			return nil, fmt.Errorf("read knowledge %s: %w", id, err)
		}
		rows, err := db.QueryContext(ctx, `SELECT kind, ref_id::text, coalesce(note, ''), created_at FROM knowledge_evidence
		  WHERE knowledge_id = $1::uuid ORDER BY created_at, id`, id)
		if err != nil {
			return nil, fmt.Errorf("read the evidence of %s: %w", id, err)
		}
		var evs []knowledge.Evidence
		for rows.Next() {
			var ev knowledge.Evidence
			var note string
			if err := rows.Scan(&ev.Kind, &ev.RefID, &note, &ev.At); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan the evidence of %s: %w", id, err)
			}
			ev.At = ev.At.UTC()
			switch ev.Kind {
			case knowledge.EvidenceCustomerReaction:
				ev.Polarity = strings.TrimPrefix(note, "polarity=")
			case knowledge.EvidenceBusinessOutcome:
				ev.OutcomeType = strings.TrimPrefix(note, "outcome=")
			case knowledge.EvidenceCounterexample:
				ev.Note = note
			}
			evs = append(evs, ev)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, carried{K: kn, Evidence: evs})
	}
	return out, nil
}

func seedCarried(ctx context.Context, db *sql.DB, rules knowledge.Rules, items []carried) (int, error) {
	n := 0
	for _, it := range items {
		if it.At.IsZero() {
			return n, fmt.Errorf("carried knowledge %s needs its replay time as its created_at", it.K.ID)
		}
		kn := it.K
		kn.Key, kn.Status, kn.Counts = nil, knowledge.StatusCandidate, knowledge.Counts{}
		kn.SupportingDecisionEpisodeIDs, kn.Counterexamples, kn.StatusHistory, kn.LastValidatedAt = nil, nil, nil, nil
		kn.CreatedAt = it.At.UTC()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return n, err
		}
		if _, err = knowledgestore.InsertReplay(ctx, tx, kn, "carried from the earlier case"); err != nil {
			_ = tx.Rollback()
			return n, fmt.Errorf("seed knowledge %s: %w", kn.ID, err)
		}
		for _, ev := range it.Evidence {
			if _, _, err = knowledgestore.RecordEvidence(ctx, tx, kn.ID, ev, rules); err != nil {
				_ = tx.Rollback()
				return n, fmt.Errorf("replay %s evidence on %s: %w", ev.Kind, kn.ID, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
