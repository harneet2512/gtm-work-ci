package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// Ingester is what IngestAll drives; *Service implements it.
type Ingester interface {
	Ingest(ctx context.Context, ev normalize.SourceEvent) (Result, error)
}

// NamedEvent is a SourceEvent together with where it was read from, for error messages.
type NamedEvent struct {
	File  string
	Index int
	Event normalize.SourceEvent
}

// Summary counts the outcomes of IngestAll.
type Summary struct {
	New       int
	Duplicate int
}

// DecodeEvents parses one SourceEvent object or an array of them, rejecting unknown
// envelope fields and trailing data.
func DecodeEvents(data []byte) ([]normalize.SourceEvent, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("empty input")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()

	var events []normalize.SourceEvent
	switch trimmed[0] {
	case '[':
		if err := dec.Decode(&events); err != nil {
			return nil, fmt.Errorf("decode event array: %w", err)
		}
	case '{':
		var ev normalize.SourceEvent
		if err := dec.Decode(&ev); err != nil {
			return nil, fmt.Errorf("decode event: %w", err)
		}
		events = []normalize.SourceEvent{ev}
	default:
		return nil, errors.New("expected a JSON object or array of objects")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after JSON value")
	}
	return events, nil
}

// LoadEvents reads a .json file, or every .json file below a directory in lexical path
// order, returning the events in that order. It fails when nothing is found.
func LoadEvents(root string) ([]NamedEvent, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("load events: %w", err)
	}
	paths := []string{root}
	if info.IsDir() {
		paths = paths[:0]
		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".json") {
				paths = append(paths, path)
			}
			return err
		})
		if walkErr != nil {
			return nil, fmt.Errorf("load events: %w", walkErr)
		}
		sort.Strings(paths)
	}

	var out []NamedEvent
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("load events: %w", err)
		}
		events, err := DecodeEvents(raw)
		if err != nil {
			return nil, fmt.Errorf("load events: %s: %w", p, err)
		}
		for i, ev := range events {
			out = append(out, NamedEvent{File: p, Index: i, Event: ev})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("load events: no SourceEvent JSON found in %s", root)
	}
	return out, nil
}

// IngestAll ingests events in order and stops at the first failure, naming the event.
// The returned Summary covers the events processed before the failure.
func IngestAll(ctx context.Context, ing Ingester, events []NamedEvent) (Summary, error) {
	var sum Summary
	for _, ne := range events {
		res, err := ing.Ingest(ctx, ne.Event)
		if err != nil {
			return sum, fmt.Errorf("%s#%d (%s/%s/%s): %w", ne.File, ne.Index,
				ne.Event.SourceSystem, ne.Event.SourceObjectID, ne.Event.SourceEventKey, err)
		}
		if res.Duplicate {
			sum.Duplicate++
		} else {
			sum.New++
		}
	}
	return sum, nil
}
