// Package corectx serves the bounded pull-context tools of GET /internal/ctx/{tool}
// (contracts/openapi/core.yaml) to the draft agent.
//
// Safety properties, each tested:
//   - the account comes from the run, never from the caller: no tool takes an account argument;
//   - a run only reads while it is pending, context_built or drafted;
//   - every served packet is bounded (items, bytes, per-item clipping) and logged to
//     context_access_log before it is returned, so AgentRun.input_context_refs cannot be forged;
//   - a run may make at most MaxPulls pulls.
//
// The package only reads domain tables; its single write is the access-log row.
package corectx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/visibility"
)

// Bounds of one packet (contracts/schemas/context_packet.v1.json, core.yaml pullContext).
const (
	DefaultLimit = 10
	MaxLimit     = 20
	// MaxPacketBytes bounds the serialized items. The worker's default response cap is 16 KiB.
	MaxPacketBytes = 12 << 10
	// MaxPulls is the most context pulls one run may make.
	MaxPulls = 50
)

// Tool is one pull tool.
type Tool string

// The six tools of the draft agent (context_access_log CHECK, core.yaml enum).
const (
	ToolState       Tool = "state"
	ToolRecentDiffs Tool = "recent_diffs"
	ToolEvidence    Tool = "evidence"
	ToolActivities  Tool = "activities"
	ToolPeople      Tool = "people"
	ToolCommitments Tool = "commitments"
	// ToolGraphNeighborhood is the bounded Neo4j neighborhood of the run's account (ids and evidence
	// refs only; Postgres serves the exact evidence). It exists only when a GraphReader is configured.
	ToolGraphNeighborhood Tool = "graph_neighborhood"
)

var tools = map[Tool]bool{ToolState: true, ToolRecentDiffs: true, ToolEvidence: true, ToolActivities: true, ToolPeople: true, ToolCommitments: true,
	ToolGraphNeighborhood: true}

// Errors a caller maps to HTTP statuses.
var (
	// ErrUnknownTool: no such tool (404).
	ErrUnknownTool = errors.New("corectx: unknown tool")
	// ErrBadParams: a parameter is out of range or missing (400).
	ErrBadParams = errors.New("corectx: bad parameters")
	// ErrRunInactive: the run does not exist or no longer accepts pulls (403).
	ErrRunInactive = errors.New("corectx: run does not accept context pulls")
	// ErrTooManyPulls: the run used its pull budget (429).
	ErrTooManyPulls = errors.New("corectx: pull budget of the run is used up")
	// ErrAmbiguousWorldTime: a non-trigger activity of the account occurred at the same instant as the
	// run's newest trigger, so "what the run could see" cannot be told from arrival order (409). The pull
	// fails closed rather than serve data that may be later than the trigger (ADR-0019 section 3).
	ErrAmbiguousWorldTime = errors.New("corectx: an activity of the account ties with the run's trigger in world time")
	// ErrStateAfterCutoff: the state version the run is pinned to folds an activity newer than the run's
	// trigger (a coalesced burst), so serving it would show the run its own future (409).
	ErrStateAfterCutoff = errors.New("corectx: the run's state version folds activities after its trigger")
)

// GraphReader serves the graph_neighborhood tool; *ctxgraph.Reader implements it. hidden are the
// account's non-org activity ids (the same set the other tools withhold).
type GraphReader interface {
	// NeighborhoodItemsAsOf is the packet for a run whose world is cut at t (ADR-0019): the graph as it
	// stood strictly before t, rebuilt from Postgres, without the decision episodes on ownTrigger (the
	// run's newest trigger time) or later.
	NeighborhoodItemsAsOf(ctx context.Context, accountID string, limit int, hidden map[string]bool, t, ownTrigger time.Time) (items []any, truncated bool, err error)
}

// acceptingStatuses are the run states in which a draft agent may still read context.
var acceptingStatuses = map[string]bool{"pending": true, "context_built": true, "drafted": true}

// Params are the only arguments a pull takes (no account parameter, by design).
type Params struct {
	FieldPath string
	Limit     int
}

// Limits echoes the bounds applied to a packet.
type Limits struct {
	MaxItems int `json:"max_items"`
	MaxBytes int `json:"max_bytes"`
}

// Packet is contracts/schemas/context_packet.v1.json.
type Packet struct {
	AccessID  int64             `json:"access_id"`
	Tool      Tool              `json:"tool"`
	Items     []json.RawMessage `json:"items"`
	Truncated bool              `json:"truncated"`
	Bytes     int               `json:"bytes"`
	// WorldAsOf is the world-time cutoff the pull was served at (ADR-0019): strictly before it, so the
	// run's trigger activity (cutoff = its occurred_at + 1 µs) is the newest thing visible. Always set.
	WorldAsOf *time.Time `json:"world_as_of"`
	Limits    Limits     `json:"limits"`
}

// Service runs pulls against db.
type Service struct {
	db    *sql.DB
	graph GraphReader
}

// Option configures a Service.
type Option func(*Service)

// WithGraph enables the graph_neighborhood tool.
func WithGraph(g GraphReader) Option { return func(s *Service) { s.graph = g } }

// New returns a Service on db.
func New(db *sql.DB, opts ...Option) (*Service, error) {
	if db == nil {
		return nil, errors.New("corectx: database is required")
	}
	s := &Service{db: db}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// runScope is what a pull learns about its run.
type runScope struct {
	runID      string
	accountID  string
	triggerIDs []string
	// cutoff is the world time the run reads at and trigger the occurred_at of its newest trigger (see
	// worldCutoff); every pull is world-cut.
	cutoff, trigger time.Time
	// stateVersion is the state version the run was built on (agent_runs.state_version), when recorded.
	stateVersion sql.NullInt32
}

// Pull serves one tool for the run and logs it. The account is the run's.
func (s *Service) Pull(ctx context.Context, runID string, tool Tool, p Params) (Packet, error) {
	if !tools[tool] || (tool == ToolGraphNeighborhood && s.graph == nil) {
		return Packet{}, ErrUnknownTool
	}
	p, err := checkParams(tool, p)
	if err != nil {
		return Packet{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Packet{}, fmt.Errorf("corectx: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	scope, err := lockRun(ctx, tx, runID)
	if err != nil {
		return Packet{}, err
	}
	hidden, err := visibility.HiddenActivities(ctx, tx, scope.accountID)
	if err != nil {
		return Packet{}, err
	}
	items, more, err := s.fetchTool(ctx, tx, scope, tool, p, hidden)
	if err != nil {
		return Packet{}, err
	}
	b := bound(items, p.Limit, hidden)
	packet := Packet{Tool: tool, Items: b.items, Truncated: more || b.truncated, Bytes: b.bytes, WorldAsOf: &scope.cutoff,
		Limits: Limits{MaxItems: p.Limit, MaxBytes: MaxPacketBytes}}
	if packet.AccessID, err = logAccess(ctx, tx, scope, packet, p, b.ids); err != nil {
		return Packet{}, err
	}
	if err := tx.Commit(); err != nil {
		return Packet{}, fmt.Errorf("corectx: commit: %w", err)
	}
	return packet, nil
}

// fetchTool serves the graph tool from the world graph and every other tool from Postgres. No tool reads
// Neo4j, so no pull waits for the projection.
func (s *Service) fetchTool(ctx context.Context, tx *sql.Tx, sc runScope, tool Tool, p Params, hidden map[string]bool) ([]any, bool, error) {
	if tool == ToolGraphNeighborhood {
		return s.graph.NeighborhoodItemsAsOf(ctx, sc.accountID, p.Limit, hidden, sc.cutoff, sc.trigger)
	}
	return fetch(ctx, tx, sc, tool, p)
}

func checkParams(tool Tool, p Params) (Params, error) {
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	if p.Limit < 1 || p.Limit > MaxLimit {
		return p, fmt.Errorf("limit must be between 1 and %d: %w", MaxLimit, ErrBadParams)
	}
	if p.FieldPath != "" && !claimFieldPaths[p.FieldPath] {
		return p, fmt.Errorf("field_path is not an account-state field path: %w", ErrBadParams)
	}
	if tool == ToolEvidence && p.FieldPath == "" {
		return p, fmt.Errorf("the evidence tool needs field_path: %w", ErrBadParams)
	}
	return p, nil
}

// lockRun serializes the run's pulls (advisory lock held to commit), checks that it still accepts
// pulls and that its budget is not used up, and returns its scope.
func lockRun(ctx context.Context, tx *sql.Tx, runID string) (runScope, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "ctx:"+runID); err != nil {
		return runScope{}, fmt.Errorf("corectx: lock run: %w", err)
	}
	var status string
	var ids []byte
	sc := runScope{runID: runID}
	err := tx.QueryRowContext(ctx, `SELECT account_id::text, status, to_jsonb(trigger_activity_ids), state_version
 FROM agent_runs WHERE id = $1::uuid FOR SHARE`, runID).Scan(&sc.accountID, &status, &ids, &sc.stateVersion)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !acceptingStatuses[status]) {
		return sc, ErrRunInactive
	}
	if err != nil {
		return sc, fmt.Errorf("corectx: load run: %w", err)
	}
	if err := json.Unmarshal(ids, &sc.triggerIDs); err != nil {
		return sc, fmt.Errorf("corectx: decode trigger activities: %w", err)
	}
	if sc.cutoff, sc.trigger, err = worldCutoff(ctx, tx, sc.accountID, sc.triggerIDs); err != nil {
		return sc, err
	}
	var pulls int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM context_access_log WHERE agent_run_id = $1::uuid`, runID).Scan(&pulls); err != nil {
		return sc, fmt.Errorf("corectx: count pulls: %w", err)
	}
	if pulls >= MaxPulls {
		return sc, ErrTooManyPulls
	}
	return sc, nil
}

func logAccess(ctx context.Context, tx *sql.Tx, sc runScope, packet Packet, p Params, ids []string) (int64, error) {
	args := map[string]any{}
	if p.FieldPath != "" {
		args["field_path"] = p.FieldPath
	}
	args["limit"] = p.Limit
	args["world_as_of"] = sc.cutoff.UTC().Format(time.RFC3339Nano)
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return 0, fmt.Errorf("corectx: encode args: %w", err)
	}
	if ids == nil {
		ids = []string{}
	}
	rawIDs, err := json.Marshal(ids)
	if err != nil {
		return 0, fmt.Errorf("corectx: encode ids: %w", err)
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO context_access_log (agent_run_id, tool, args, returned_ids, bytes, truncated)
 VALUES ($1::uuid, $2, $3::jsonb, $4::jsonb, $5, $6) RETURNING id`,
		sc.runID, string(packet.Tool), string(rawArgs), string(rawIDs), packet.Bytes, packet.Truncated).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("corectx: log access: %w", err)
	}
	return id, nil
}

// uuidList renders ids for the bound parameter of `string_to_array($n, ',')::uuid[]`. Anything
// that is not a uuid is dropped so a bad value can never fail the cast.
func uuidList(ids []string) string {
	keep := make([]string, 0, len(ids))
	for _, id := range ids {
		if readmodel.ValidUUID(id) {
			keep = append(keep, id)
		}
	}
	return strings.Join(keep, ",")
}
