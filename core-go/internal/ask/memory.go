package ask

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Conversation memory (owner request, 2026-10-06). Core, not the Slack adapter, keeps the turns of a DM or a thread,
// so a follow-up such as "why?" or "and EcoLite?" resolves against what was said before.
const (
	// VerbatimTurns is how many of the latest turns the worker sees word for word.
	VerbatimTurns = 8
	// MaxStoredTurns bounds what is loaded for one conversation; older turns are gone for good.
	MaxStoredTurns = 60
	maxHistoryText = 2000 // the worker's limit of one history entry
	summaryLine    = 140  // characters kept of each older turn in the summary
	summaryLimit   = 1500
	pendingTTL     = 30 * time.Minute
)

// Turn roles.
const (
	RoleUser  = "user"
	RoleCliff = "cliff"
)

// ErrNotFound: no such trace.
var ErrNotFound = errors.New("ask: not found")

// Turn is one thing said in a conversation.
type Turn struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Pending is a task paused on a state-changing step: the question being worked on and the action awaiting its Run.
type Pending struct {
	Kind        string    `json:"kind"`
	Question    string    `json:"question"`
	ChannelKind string    `json:"channel_kind"`
	At          time.Time `json:"at"`
}

// Store is where core keeps conversations, paused tasks and traces. Postgres in the running core
// (internal/askstore); MemoryStore for tests and a core with no database.
type Store interface {
	// Turns returns the latest turns of a conversation, oldest first, at most limit.
	Turns(ctx context.Context, ref string, limit int) ([]Turn, error)
	AppendTurns(ctx context.Context, ref string, turns ...Turn) error
	// SetPending records the task paused on an action; nil clears it.
	SetPending(ctx context.Context, ref string, p *Pending) error
	// Pending returns the paused task, or nil.
	Pending(ctx context.Context, ref string) (*Pending, error)
	SaveTrace(ctx context.Context, t Trace) error
	// Trace returns ErrNotFound for an unknown id.
	Trace(ctx context.Context, id string) (Trace, error)
}

// MemoryStore is the in-process Store.
type MemoryStore struct {
	mu      sync.Mutex
	turns   map[string][]Turn
	pending map[string]Pending
	traces  map[string]Trace
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{turns: map[string][]Turn{}, pending: map[string]Pending{}, traces: map[string]Trace{}}
}

func (m *MemoryStore) Turns(_ context.Context, ref string, limit int) ([]Turn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.turns[ref]
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	return append([]Turn(nil), all...), nil
}

func (m *MemoryStore) AppendTurns(_ context.Context, ref string, turns ...Turn) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns[ref] = append(m.turns[ref], turns...)
	return nil
}

func (m *MemoryStore) SetPending(_ context.Context, ref string, p *Pending) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == nil {
		delete(m.pending, ref)
		return nil
	}
	m.pending[ref] = *p
	return nil
}

func (m *MemoryStore) Pending(_ context.Context, ref string) (*Pending, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.pending[ref]; ok {
		return &p, nil
	}
	return nil, nil
}

func (m *MemoryStore) SaveTrace(_ context.Context, t Trace) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.traces[t.ID] = t
	return nil
}

func (m *MemoryStore) Trace(_ context.Context, id string) (Trace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.traces[id]; ok {
		return t, nil
	}
	return Trace{}, ErrNotFound
}

// BuildHistory is what the worker is shown: older turns condensed into one summary entry (their first words, so a
// name or a date mentioned long ago is not lost), then the last VerbatimTurns word for word. Deterministic: no
// model call is spent on memory.
func BuildHistory(turns []Turn) []HistoryTurn {
	var out []HistoryTurn
	if n := len(turns) - VerbatimTurns; n > 0 {
		out = append(out, HistoryTurn{Role: "summary", Text: summarise(turns[:n])})
		turns = turns[n:]
	}
	for _, t := range turns {
		out = append(out, HistoryTurn{Role: t.Role, Text: clipRunes(t.Text, maxHistoryText)})
	}
	return out
}

func summarise(older []Turn) string {
	var b strings.Builder
	b.WriteString("Earlier in this conversation: ")
	for i, t := range older {
		who := "asked"
		if t.Role == RoleCliff {
			who = "answered"
		}
		line := strings.Join(strings.Fields(t.Text), " ")
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(who + " " + clipRunes(line, summaryLine))
		if b.Len() > summaryLimit {
			break
		}
	}
	return clipRunes(b.String(), summaryLimit)
}
