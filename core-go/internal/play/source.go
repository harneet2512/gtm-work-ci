package play

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// ErrEventNotInSource: the replay dataset does not hold the manifest's held-out event.
var ErrEventNotInSource = errors.New("play: the held-out event is not in the replay dataset")

// EventSource is the replay dataset: it hands out the SourceEvent of an event by its idempotency key. The
// held-out event is deliberately not in any store before Play; it lives only here.
type EventSource interface {
	Lookup(ctx context.Context, ref SourceRef) (normalize.SourceEvent, error)
}

// FileSource serves SourceEvents from a JSON file, or every .json file below a directory
// (GHOST_REPLAY_EVENTS). The files are read on first use, so a missing dataset fails Play with a clear
// error instead of failing core at startup. A failed read is not remembered: the next lookup reads again, so
// fixing the dataset needs no restart. Two events with the same idempotency key are an error, never a silent
// overwrite: which payload Play would release must not depend on file order.
type FileSource struct {
	path string
	mu   sync.Mutex
	byID map[SourceRef]normalize.SourceEvent // nil until a read succeeded
}

// NewFileSource returns a source over path.
func NewFileSource(path string) (*FileSource, error) {
	if path == "" {
		return nil, errors.New("play: the replay events path is empty")
	}
	return &FileSource{path: path}, nil
}

// index returns the dataset keyed by idempotency key, reading it on first use and again after a failure.
func (s *FileSource) index() (map[SourceRef]normalize.SourceEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID != nil {
		return s.byID, nil
	}
	events, err := ingest.LoadEvents(s.path)
	if err != nil {
		return nil, fmt.Errorf("play: load replay events from %s: %w", s.path, err)
	}
	byID := make(map[SourceRef]normalize.SourceEvent, len(events))
	where := make(map[SourceRef]string, len(events))
	for _, ne := range events {
		ev := ne.Event
		ref := SourceRef{System: ev.SourceSystem, ObjectID: ev.SourceObjectID, EventKey: ev.SourceEventKey}
		here := fmt.Sprintf("%s[%d]", ne.File, ne.Index)
		if first, dup := where[ref]; dup {
			return nil, fmt.Errorf("play: the replay dataset %s holds %s/%s/%s twice (%s and %s)", s.path, ref.System, ref.ObjectID, ref.EventKey, first, here)
		}
		where[ref], byID[ref] = here, ev
	}
	s.byID = byID
	return byID, nil
}

// Lookup implements EventSource.
func (s *FileSource) Lookup(_ context.Context, ref SourceRef) (normalize.SourceEvent, error) {
	byID, err := s.index()
	if err != nil {
		return normalize.SourceEvent{}, err
	}
	ev, ok := byID[ref]
	if !ok {
		return normalize.SourceEvent{}, fmt.Errorf("%w: %s/%s/%s", ErrEventNotInSource, ref.System, ref.ObjectID, ref.EventKey)
	}
	return ev, nil
}
