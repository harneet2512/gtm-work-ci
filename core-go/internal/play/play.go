package play

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/biwriter"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// Defaults for the wait on the pipeline.
const (
	DefaultPoll    = 200 * time.Millisecond
	DefaultTimeout = 50 * time.Second // inside the API's PlayTimeout (55 s) and the server's write timeout (60 s)
)

// Errors the API maps to statuses.
var (
	// ErrAlreadyReleased: Play completed before; nothing changed (409). The error returned is an
	// *AlreadyReleasedError naming the change the first Play wrote.
	ErrAlreadyReleased = errors.New("play: the held-out event was already released")
	// ErrWrongEvent: the request names an event that is not the manifest's held-out event (422).
	ErrWrongEvent = errors.New("play: the event is not the manifest's held-out event")
	// ErrReleaseMismatch: the dataset's event does not match the manifest, or the pipeline attributed it
	// elsewhere (422).
	ErrReleaseMismatch = errors.New("play: the released event does not match the manifest")
	// ErrSourceUnavailable: the replay dataset is not configured or cannot supply the event (503).
	ErrSourceUnavailable = errors.New("play: the replay dataset cannot supply the held-out event")
	// ErrPlayInProgress: another Play of the manifest is running; try again (409).
	ErrPlayInProgress = errors.New("play: another Play of this manifest is running")
	// ErrTimeout: the recompute or the projection did not finish in time; calling Play again resumes (504).
	ErrTimeout = errors.New("play: the pipeline did not finish in time")
)

// AlreadyReleasedError is ErrAlreadyReleased with the AccountChange the completed Play wrote, so the caller that
// lost track of its state can fetch the result instead of guessing.
type AlreadyReleasedError struct{ AccountChangeID string }

func (e *AlreadyReleasedError) Error() string {
	return fmt.Sprintf("%v (account change %s)", ErrAlreadyReleased, e.AccountChangeID)
}

// Is makes errors.Is(err, ErrAlreadyReleased) true.
func (e *AlreadyReleasedError) Is(target error) bool { return target == ErrAlreadyReleased }

// VisibleError is the refusal of a Play whose event is already visible (422 held_out_event_visible).
type VisibleError struct{ Report Report }

func (e *VisibleError) Error() string {
	return fmt.Sprintf("play: refusing to release event %s: %d thing(s) derived from it already exist", e.Report.HeldOutEventID, len(e.Report.Leaks))
}

// Ingester is the ingest path of core; *ingest.Service implements it.
type Ingester interface {
	Ingest(ctx context.Context, ev normalize.SourceEvent) (ingest.Result, error)
}

// Recomputer runs due recompute jobs; *coalesce.Service implements it. Optional: when nil, Play waits for
// the background coalescer of core.
type Recomputer interface {
	Drain(ctx context.Context) (coalesce.DrainResult, error)
}

// Barrier waits for an account's graph projection to finish; *ctxgraph.Barrier implements it.
type Barrier interface {
	Wait(ctx context.Context, accountID string, poll time.Duration) error
}

// Options configures a Service.
type Options struct {
	DB        *sql.DB
	Ingest    Ingester
	Events    EventSource
	Recompute Recomputer
	// Graph and Probe are the Neo4j side: the projection barrier and the invisibility probe. Without them Play
	// refuses with ErrGraphUnavailable before releasing anything.
	Graph   Barrier
	Probe   GraphProbe
	Clock   clock.Clock
	Poll    time.Duration
	Timeout time.Duration
	// Rules are the knowledge lifecycle rules the episode view's knowledge-as-of read replays (HAR-117).
	// Nil means the deployment has none: the view then fails closed with ErrKnowledgeUnavailable rather
	// than answer with knowledge it cannot age.
	Rules *knowledge.Rules
	// HistoricalEnd splits the sequence into the historical-learning window 1..h and the held-out live
	// window h+1..N (HAR-129 §B; GHOST_REPLAY_HISTORICAL_END). Nil means "all of history": h = N-1.
	HistoricalEnd *int
}

// Service releases held-out events and reports on their invisibility.
type Service struct {
	db            *sql.DB
	ingest        Ingester
	events        EventSource
	drain         Recomputer
	graph         Barrier
	checker       *Checker
	probe         GraphProbe
	clk           clock.Clock
	poll          time.Duration
	timeout       time.Duration
	rules         *knowledge.Rules
	historicalEnd *int
}

// NewService validates the options.
func NewService(o Options) (*Service, error) {
	if o.DB == nil || o.Ingest == nil {
		return nil, errors.New("play: a database and an ingest service are required")
	}
	if err := o.checkReplay(); err != nil {
		return nil, err
	}
	checker, err := NewChecker(o.DB, o.Probe)
	if err != nil {
		return nil, err
	}
	s := &Service{db: o.DB, ingest: o.Ingest, events: o.Events, drain: o.Recompute, graph: o.Graph, checker: checker, probe: o.Probe,
		clk: o.Clock, poll: o.Poll, timeout: o.Timeout, rules: o.Rules, historicalEnd: o.HistoricalEnd}
	if s.clk == nil {
		s.clk = clock.Real{}
	}
	if s.poll <= 0 {
		s.poll = DefaultPoll
	}
	if s.timeout <= 0 {
		s.timeout = DefaultTimeout
	}
	return s, nil
}

// Request is POST /replay/play.
type Request struct {
	ManifestID string `json:"manifest_id"`
	EventID    string `json:"event_id,omitempty"`
}

// Invisibility runs the event-N-invisible assertion for a manifest (GET /replay/manifests/{id}/invisibility).
func (s *Service) Invisibility(ctx context.Context, manifestID string) (Report, error) {
	m, err := LoadManifest(ctx, s.db, manifestID)
	if err != nil {
		return Report{}, err
	}
	return s.checker.Check(ctx, m)
}

// Play releases the manifest's held-out event and returns the PlayResult document (core.yaml PlayResult).
//
// Order: refuse what cannot work before anything is released (wrong event, no graph, no dataset event);
// run the invisibility assertion; record the release; ingest event N through the normal path; wait for the
// recompute and the graph projection; write the change and its update and complete the record in one
// transaction. Plays of one manifest never overlap: a second one is refused with ErrPlayInProgress while the first runs and with
// ErrAlreadyReleased once it has completed.
func (s *Service) Play(ctx context.Context, req Request) ([]byte, error) {
	m, err := LoadManifest(ctx, s.db, req.ManifestID)
	if err != nil {
		return nil, err
	}
	if req.EventID != "" && req.EventID != m.Held.EventID {
		return nil, ErrWrongEvent
	}
	unlock, err := s.lock(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	rec, err := s.record(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	if rec.complete {
		return nil, &AlreadyReleasedError{AccountChangeID: rec.changeID}
	}
	if s.graph == nil || s.probe == nil {
		return nil, fmt.Errorf("%w: the graph projection is not configured", ErrGraphUnavailable)
	}
	ev, err := s.dataset(ctx, m)
	if err != nil {
		return nil, err
	}
	if !rec.started {
		if err := s.assertInvisibleAndRecord(ctx, m); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	src, err := s.release(ctx, m, ev)
	if err != nil {
		return nil, err
	}
	facts, err := s.awaitFacts(ctx, src, m.AccountID)
	if err != nil {
		return nil, err
	}
	changeID, err := s.complete(ctx, m, facts)
	if err != nil {
		return nil, err
	}
	return s.result(ctx, m, changeID)
}

type playRecord struct {
	started, complete bool
	changeID          string
}

func (s *Service) record(ctx context.Context, manifestID string) (playRecord, error) {
	var status string
	var change sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT status, account_change_id::text FROM demo_plays WHERE manifest_id = $1::uuid`, manifestID).Scan(&status, &change)
	if errors.Is(err, sql.ErrNoRows) {
		return playRecord{}, nil
	}
	if err != nil {
		return playRecord{}, fmt.Errorf("play: read the play record of %s: %w", manifestID, err)
	}
	return playRecord{started: true, complete: status == "complete", changeID: change.String}, nil
}

// awaitFacts waits until the recompute has written the diff and the projection has recorded the graph diff,
// then loads the facts. It runs due recompute jobs itself when it has a Recomputer.
func (s *Service) awaitFacts(ctx context.Context, src biwriter.Source, accountID string) (biwriter.Facts, error) {
	for {
		if s.drain != nil {
			if _, err := s.drain.Drain(ctx); err != nil && ctx.Err() == nil {
				return biwriter.Facts{}, fmt.Errorf("play: recompute: %w", err)
			}
		}
		f, err := biwriter.LoadFacts(ctx, s.db, src)
		switch {
		case err == nil:
			return f, nil
		case errors.Is(err, biwriter.ErrGraphPending):
			if werr := s.graph.Wait(ctx, accountID, s.poll); werr != nil && ctx.Err() == nil {
				return biwriter.Facts{}, werr
			}
		case !errors.Is(err, biwriter.ErrDiffPending):
			return biwriter.Facts{}, err
		}
		select {
		case <-ctx.Done():
			return biwriter.Facts{}, fmt.Errorf("%w: %v", ErrTimeout, err)
		case <-time.After(s.poll):
		}
	}
}

// complete builds and writes the change and its update and completes the play record, in one transaction.
func (s *Service) complete(ctx context.Context, m Manifest, f biwriter.Facts) (string, error) {
	res, err := biwriter.Build(f, biwriter.IDs{Change: newID(), BI: newID()}, s.clk.Now())
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("play: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	st, err := biwriter.Write(ctx, tx, f, res)
	if err != nil {
		return "", err
	}
	done, err := tx.ExecContext(ctx, `UPDATE demo_plays SET status = 'complete', account_change_id = $2::uuid, completed_at = $3
 WHERE manifest_id = $1::uuid AND status = 'released'`, m.ID, st.ChangeID, s.clk.Now())
	if err != nil {
		return "", fmt.Errorf("play: complete the play record: %w", err)
	}
	if n, err := done.RowsAffected(); err != nil || n != 1 {
		// Nothing is committed: no change or update without a play record that was open to complete.
		return "", fmt.Errorf("play: the play record of manifest %s is not open (%d row(s) completed, err %v): nothing was written", m.ID, n, err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("play: commit: %w", err)
	}
	return st.ChangeID, nil
}

// result assembles the PlayResult from what is stored.
func (s *Service) result(ctx context.Context, m Manifest, changeID string) ([]byte, error) {
	change, update, err := biwriter.Read(ctx, s.db, changeID)
	if err != nil {
		return nil, err
	}
	world, err := s.world(ctx, m, true)
	if err != nil {
		return nil, err
	}
	if update == nil {
		update = []byte("null")
	}
	return json.Marshal(map[string]json.RawMessage{"replay_world": world, "account_change": change, "business_intelligence_update": update})
}
