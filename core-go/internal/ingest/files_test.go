package ingest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

const oneEvent = `{"source_system":"email","source_object_id":"%s","source_event_key":"received","payload":{"kind":"email"}}`

func eventJSON(id string) string { return strings.Replace(oneEvent, "%s", id, 1) }

func TestDecodeEvents(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantIDs []string
		wantErr bool
	}{
		{"single object", eventJSON("a"), []string{"a"}, false},
		{"array", "[" + eventJSON("a") + "," + eventJSON("b") + "]", []string{"a", "b"}, false},
		{"surrounding whitespace", "\n  " + eventJSON("a") + "\n", []string{"a"}, false},
		{"empty array", "[]", nil, false},
		{"empty input", "  ", nil, true},
		{"scalar", `"hi"`, nil, true},
		{"unknown envelope field", `{"source_system":"email","source_object_id":"a","source_event_key":"k","payload":{},"extra":1}`, nil, true},
		{"trailing data", eventJSON("a") + `{}`, nil, true},
		{"truncated", `{"source_system":`, nil, true},
		{"bad timestamp", `{"source_system":"email","source_object_id":"a","source_event_key":"k","occurred_at":"soon","payload":{}}`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ingest.DecodeEvents([]byte(tc.in))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			var ids []string
			for _, ev := range got {
				ids = append(ids, ev.SourceObjectID)
			}
			if strings.Join(ids, ",") != strings.Join(tc.wantIDs, ",") {
				t.Fatalf("ids = %v, want %v", ids, tc.wantIDs)
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEventsReadsDirectoriesInLexicalOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "b.json"), eventJSON("b"))
	writeFile(t, filepath.Join(dir, "a.json"), "["+eventJSON("a1")+","+eventJSON("a2")+"]")
	writeFile(t, filepath.Join(dir, "sub", "c.json"), eventJSON("c"))
	writeFile(t, filepath.Join(dir, "a.json.bak"), "not json")
	writeFile(t, filepath.Join(dir, "notes.txt"), "ignored")

	got, err := ingest.LoadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, ne := range got {
		order = append(order, ne.Event.SourceObjectID)
	}
	if strings.Join(order, ",") != "a1,a2,b,c" {
		t.Fatalf("order = %v, want a1,a2,b,c", order)
	}
	if got[1].Index != 1 || filepath.Base(got[1].File) != "a.json" {
		t.Errorf("provenance of second event = %s#%d", got[1].File, got[1].Index)
	}
}

func TestLoadEventsAcceptsASingleFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "one.json")
	writeFile(t, file, eventJSON("only"))
	got, err := ingest.LoadEvents(file)
	if err != nil || len(got) != 1 || got[0].Event.SourceObjectID != "only" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestLoadEventsErrors(t *testing.T) {
	if _, err := ingest.LoadEvents(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing path accepted")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bad.json"), `{"nope":`)
	_, err := ingest.LoadEvents(dir)
	if err == nil || !strings.Contains(err.Error(), "bad.json") {
		t.Errorf("error should name the offending file, got %v", err)
	}
	if _, err := ingest.LoadEvents(t.TempDir()); err == nil {
		t.Error("directory without events accepted")
	}
}

type fakeIngester struct {
	calls int
	fn    func(ev normalize.SourceEvent) (ingest.Result, error)
}

func (f *fakeIngester) Ingest(_ context.Context, ev normalize.SourceEvent) (ingest.Result, error) {
	f.calls++
	return f.fn(ev)
}

func TestIngestAllCountsNewAndDuplicates(t *testing.T) {
	events := []ingest.NamedEvent{
		{File: "a.json", Index: 0, Event: normalize.SourceEvent{SourceObjectID: "n1"}},
		{File: "a.json", Index: 1, Event: normalize.SourceEvent{SourceObjectID: "d1"}},
		{File: "b.json", Index: 0, Event: normalize.SourceEvent{SourceObjectID: "n2"}},
	}
	ing := &fakeIngester{fn: func(ev normalize.SourceEvent) (ingest.Result, error) {
		return ingest.Result{Duplicate: strings.HasPrefix(ev.SourceObjectID, "d")}, nil
	}}
	sum, err := ingest.IngestAll(context.Background(), ing, events)
	if err != nil || sum.New != 2 || sum.Duplicate != 1 {
		t.Fatalf("summary %+v, err %v", sum, err)
	}
}

func TestIngestAllStopsAtFirstErrorAndNamesTheEvent(t *testing.T) {
	events := []ingest.NamedEvent{
		{File: "a.json", Index: 0, Event: normalize.SourceEvent{SourceObjectID: "ok"}},
		{File: "b.json", Index: 3, Event: normalize.SourceEvent{SourceObjectID: "boom"}},
		{File: "c.json", Index: 0, Event: normalize.SourceEvent{SourceObjectID: "never"}},
	}
	boom := errors.New("boom")
	ing := &fakeIngester{fn: func(ev normalize.SourceEvent) (ingest.Result, error) {
		if ev.SourceObjectID == "boom" {
			return ingest.Result{}, boom
		}
		return ingest.Result{}, nil
	}}
	sum, err := ingest.IngestAll(context.Background(), ing, events)
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "b.json") || !strings.Contains(err.Error(), "#3") {
		t.Fatalf("err = %v", err)
	}
	if sum.New != 1 || ing.calls != 2 {
		t.Errorf("summary %+v after %d calls", sum, ing.calls)
	}
}
