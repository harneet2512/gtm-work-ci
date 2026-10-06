package play

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const datasetJSON = `[
 {"source_system":"email","source_object_id":"<a@x>","source_event_key":"received","occurred_at":"2026-09-29T15:42:00Z",
  "payload":{"kind":"email","direction":"inbound","message_id":"<a@x>","thread_id":"t","from":{"email":"priya.shah@acme.com"},
  "to":[{"email":"dana@vendor.example"}],"date":"2026-09-29T15:42:00Z","subject":"s","body_text":"b"}},
 {"source_system":"email","source_object_id":"<a@x>","source_event_key":"sent","occurred_at":"2026-09-29T15:43:00Z",
  "payload":{"kind":"email","direction":"outbound","message_id":"<a@x>","thread_id":"t","from":{"email":"dana@vendor.example"},
  "to":[{"email":"priya.shah@acme.com"}],"date":"2026-09-29T15:43:00Z","subject":"s","body_text":"b"}}]`

func TestFileSourceFindsAnEventByItsIdempotencyKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	if err := os.WriteFile(path, []byte(datasetJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := NewFileSource(path)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := src.Lookup(bg, SourceRef{System: "email", ObjectID: "<a@x>", EventKey: "sent"})
	if err != nil || ev.SourceEventKey != "sent" || ev.OccurredAt == nil || ev.OccurredAt.Minute() != 43 {
		t.Fatalf("Lookup = %+v, %v", ev, err)
	}
	if _, err := src.Lookup(bg, SourceRef{System: "email", ObjectID: "<a@x>", EventKey: "opened"}); !errors.Is(err, ErrEventNotInSource) {
		t.Fatalf("an event the dataset does not hold: %v, want ErrEventNotInSource", err)
	}
}

func TestFileSourceReadsADirectoryAndFailsLateAndLoudly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.json"), []byte(datasetJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	src, _ := NewFileSource(dir)
	if _, err := src.Lookup(bg, SourceRef{System: "email", ObjectID: "<a@x>", EventKey: "received"}); err != nil {
		t.Fatalf("directory dataset: %v", err)
	}

	missing, err := NewFileSource(filepath.Join(dir, "nope.json"))
	if err != nil {
		t.Fatalf("a missing dataset must not fail construction (core must still start): %v", err)
	}
	if _, err := missing.Lookup(bg, SourceRef{}); err == nil || !strings.Contains(err.Error(), "nope.json") {
		t.Fatalf("a missing dataset must fail the lookup and name the file: %v", err)
	}
	if _, err := NewFileSource(""); err == nil {
		t.Fatal("an empty path is a configuration error")
	}
}

func TestFileSourceRefusesTwoEventsWithTheSameKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(datasetJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(datasetJSON), 0o600); err != nil { // the same events again
		t.Fatal(err)
	}
	src, _ := NewFileSource(dir)
	_, err := src.Lookup(bg, SourceRef{System: "email", ObjectID: "<a@x>", EventKey: "received"})
	if err == nil || !strings.Contains(err.Error(), "twice") || !strings.Contains(err.Error(), "email/<a@x>/received") ||
		!strings.Contains(err.Error(), "a.json") || !strings.Contains(err.Error(), "b.json") {
		t.Fatalf("duplicate keys must be refused, naming the key and both places: %v", err)
	}
	// within one file too
	one := filepath.Join(t.TempDir(), "dup.json")
	doubled := strings.Replace(datasetJSON, `"source_event_key":"sent"`, `"source_event_key":"received"`, 1)
	if err := os.WriteFile(one, []byte(doubled), 0o600); err != nil {
		t.Fatal(err)
	}
	src, _ = NewFileSource(one)
	if _, err := src.Lookup(bg, SourceRef{System: "email", ObjectID: "<a@x>", EventKey: "received"}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate keys within one file: %v", err)
	}
}

// A failed read is not cached: once the dataset is fixed the next lookup succeeds, with no restart.
func TestFileSourceRetriesAFailedLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.json")
	src, _ := NewFileSource(path)
	ref := SourceRef{System: "email", ObjectID: "<a@x>", EventKey: "received"}
	if _, err := src.Lookup(bg, ref); err == nil {
		t.Fatal("the dataset does not exist yet")
	}
	if err := os.WriteFile(path, []byte(`[{"source_system":`), 0o600); err != nil { // broken JSON
		t.Fatal(err)
	}
	if _, err := src.Lookup(bg, ref); err == nil {
		t.Fatal("broken JSON must fail")
	}
	if err := os.WriteFile(path, []byte(datasetJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if ev, err := src.Lookup(bg, ref); err != nil || ev.SourceEventKey != "received" {
		t.Fatalf("after the fix: %+v, %v", ev, err)
	}
	// and a good load is kept: deleting the file afterwards does not matter
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Lookup(bg, ref); err != nil {
		t.Fatalf("a successful read is cached: %v", err)
	}
}

func TestFileSourceIsSafeForConcurrentLookups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	if err := os.WriteFile(path, []byte(datasetJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	src, _ := NewFileSource(path)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			_, err := src.Lookup(bg, SourceRef{System: "email", ObjectID: "<a@x>", EventKey: "sent"})
			errs <- err
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}
