package play

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
)

// episodeBound is the world-time bound of episode k: just after event k (the reads are strict-before, so the
// event itself is included; ADR-0019). At k=0 — before any event — it is just before event 1.
func episodeBound(seq Sequence, k int) time.Time {
	if k == 0 {
		return seq.Events[0].OccurredAt.Add(-time.Microsecond)
	}
	at, _ := seq.At(k)
	return at.OccurredAt.Add(time.Microsecond)
}

// stateJSON is episode_replay.v1.json $defs/episodeState.
type stateJSON struct {
	Version  int             `json:"version"`
	AsOf     *time.Time      `json:"as_of"`
	Digest   string          `json:"digest"`
	Document json.RawMessage `json:"document"`
}

type knowledgeItemJSON struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type knowledgeJSON struct {
	AsOf  *time.Time          `json:"as_of"`
	Items []knowledgeItemJSON `json:"items"`
}

type boundaryJSON struct {
	HistoricalStart int `json:"historical_start"`
	HistoricalEnd   int `json:"historical_end"`
	LiveStart       int `json:"live_start"`
	LiveEnd         int `json:"live_end"`
}

// historicalWindow is h, the boundary's historical_end: configured via Options.HistoricalEnd, clamped to
// N-1 — the held-out event is never in the historical window — defaulting to "all of history".
func (s *Service) historicalWindow(seq Sequence) int {
	h := seq.N() - 1
	if s.historicalEnd != nil && *s.historicalEnd < h {
		h = *s.historicalEnd
	}
	return h
}

func (b boundaryJSON) window(k int) string {
	switch {
	case k == 0:
		return "none"
	case k <= b.HistoricalEnd:
		return "historical"
	default:
		return "live"
	}
}

// viewJSON is the EpisodeReplayView document of GET /replay/manifests/{id}/episodes.
type viewJSON struct {
	ManifestID    string             `json:"manifest_id"`
	AccountID     string             `json:"account_id"`
	OpportunityID *string            `json:"opportunity_id"`
	Episode       int                `json:"episode"`
	Total         int                `json:"total"`
	Boundary      boundaryJSON       `json:"boundary"`
	Window        string             `json:"window"`
	State         *stateJSON         `json:"state"`
	Knowledge     knowledgeJSON      `json:"knowledge"`
	PriorEpisodes []episodeEventJSON `json:"prior_episodes"`
	NextEvent     *episodeEventJSON  `json:"next_event"`
	CanPrevious   bool               `json:"can_previous"`
	CanPlayNext   bool               `json:"can_play_next"`
	ComputedAt    time.Time          `json:"computed_at"`
}

// Episodes is GET /replay/manifests/{id}/episodes: the view at the released cursor, or at `at` when a cursor
// parameter was passed (0 ≤ at ≤ released). Every read is world-timed at the episode bound — nothing the
// events after k caused may appear.
func (s *Service) Episodes(ctx context.Context, manifestID string, at *int) ([]byte, error) {
	seq, err := LoadSequence(ctx, s.db, manifestID)
	if err != nil {
		return nil, err
	}
	released, err := releasedCursor(ctx, s.db, manifestID)
	if err != nil {
		return nil, err
	}
	k := released
	if at != nil {
		if *at < 0 || *at > released {
			return nil, fmt.Errorf("%w: %d is not a released episode (0..%d)", ErrInvalidEpisode, *at, released)
		}
		k = *at
	}
	rows, err := loadEpisodes(ctx, s.db, manifestID)
	if err != nil {
		return nil, err
	}
	byPosition := make(map[int]episodeRow, len(rows))
	for _, r := range rows {
		byPosition[r.Position] = r
	}
	return s.view(ctx, seq, k, released, byPosition)
}

// view assembles the document at episode k. released is the replay's cursor (Play-next and the bounds ride
// on it, not on the viewed position).
func (s *Service) view(ctx context.Context, seq Sequence, k, released int, rows map[int]episodeRow) ([]byte, error) {
	bound := episodeBound(seq, k)
	st, err := s.stateAt(ctx, seq.AccountID, k, bound)
	if err != nil {
		return nil, err
	}
	kn, err := s.knowledgeAt(ctx, bound)
	if err != nil {
		return nil, err
	}
	prior := make([]episodeEventJSON, 0, k)
	for pos := 1; pos <= k; pos++ {
		row, ok := rows[pos]
		if !ok {
			return nil, fmt.Errorf("play: episode %d of %s is released but has no bookkeeping row", pos, seq.ManifestID)
		}
		ev, _ := seq.At(pos)
		prior = append(prior, eventJSON(ev, &row))
	}
	var next *episodeEventJSON
	if k < seq.N() {
		ev, _ := seq.At(k + 1)
		e := eventJSON(ev, nil) // withheld: no state, change or materiality
		next = &e
	}
	h := s.historicalWindow(seq)
	boundary := boundaryJSON{}
	if h >= 1 {
		boundary.HistoricalStart, boundary.HistoricalEnd = 1, h
	}
	if h < seq.N() {
		boundary.LiveStart, boundary.LiveEnd = h+1, seq.N()
	}
	var opp *string
	if seq.OpportunityID != "" {
		opp = &seq.OpportunityID
	}
	return json.Marshal(viewJSON{
		ManifestID:    seq.ManifestID,
		AccountID:     seq.AccountID,
		OpportunityID: opp,
		Episode:       k,
		Total:         seq.N(),
		Boundary:      boundary,
		Window:        boundary.window(k),
		State:         st,
		Knowledge:     kn,
		PriorEpisodes: prior,
		NextEvent:     next,
		CanPrevious:   k > 0,
		CanPlayNext:   released < seq.N(),
		ComputedAt:    s.clk.Now().UTC(),
	})
}

// stateAt is the account state the world knew at the episode bound (state_history, strict-before — the same
// read as Reader.StateWorldAsOf, plus the version and as_of the view reports).
func (s *Service) stateAt(ctx context.Context, accountID string, k int, bound time.Time) (*stateJSON, error) {
	if k == 0 {
		return nil, nil
	}
	var (
		version int
		asOf    time.Time
		doc     []byte
	)
	err := s.db.QueryRowContext(ctx, `SELECT version, as_of, state::text FROM state_history
	 WHERE account_id = $1::uuid AND as_of < $2 ORDER BY as_of DESC, version DESC LIMIT 1`,
		accountID, bound.UTC()).Scan(&version, &asOf, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("play: state of %s before %s: %w", accountID, bound.Format(time.RFC3339Nano), err)
	}
	digest, err := digestOf(doc)
	if err != nil {
		return nil, err
	}
	return &stateJSON{Version: version, AsOf: &asOf, Digest: digest, Document: doc}, nil
}

// knowledgeAt is the knowledge applicable at the episode bound (ListApplicableAsOf: created by then, status
// replayed from evidence up to then).
func (s *Service) knowledgeAt(ctx context.Context, bound time.Time) (knowledgeJSON, error) {
	if s.rules == nil {
		return knowledgeJSON{}, ErrKnowledgeUnavailable
	}
	items, err := knowledgestore.ListApplicableAsOf(ctx, s.db, *s.rules, bound.UTC())
	if err != nil {
		return knowledgeJSON{}, fmt.Errorf("play: knowledge as of %s: %w", bound.Format(time.RFC3339), err)
	}
	asOf := bound.UTC()
	out := knowledgeJSON{AsOf: &asOf, Items: make([]knowledgeItemJSON, 0, len(items))}
	for _, k := range items {
		out.Items = append(out.Items, knowledgeItemJSON{ID: k.ID, Title: k.Title, Status: k.Status})
	}
	return out, nil
}
