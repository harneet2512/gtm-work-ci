package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

const ingestTestdata = "../../internal/ingest/testdata"

func writeEvent(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func emailEvent(id string) string {
	return `{"source_system":"email","source_object_id":"` + id + `","source_event_key":"received","payload":{
 "kind":"email","message_id":"` + id + `","thread_id":"t","direction":"inbound",
 "from":{"email":"sam@nowhere-test.example"},"to":[{"email":"dana@ghostvendor.com"}],
 "date":"2026-10-03T10:00:00Z","subject":"hi","body_text":"hello"}}`
}

func TestIngestUsageAndLoadErrorsNeedNoDatabase(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "")
	var out bytes.Buffer

	if err := run([]string{"ingest"}, &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("missing path: %v", err)
	}
	if err := run([]string{"ingest", "a", "b"}, &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("extra args: %v", err)
	}
	err := run([]string{"ingest", filepath.Join(t.TempDir(), "missing")}, &out)
	if err == nil || strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("a missing path must be reported before the database is needed, got %v", err)
	}
	empty := t.TempDir()
	if err := run([]string{"ingest", empty}, &out); err == nil {
		t.Error("directory without events accepted")
	}
}

func TestIngestCommandAgainstTestDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	t.Setenv("DATABASE_URL", env.URL)
	fixtures := abs(t, ingestTestdata) // resolve before changing directory
	t.Chdir(t.TempDir())

	count := func(table string) int {
		t.Helper()
		var n int
		if err := env.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	reset := func() {
		t.Helper()
		if err := storetest.Purge(context.Background(), env.DB, `TRUNCATE recompute_jobs, unresolved_activities, activity_participants, activities, source_events RESTART IDENTITY CASCADE`); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("fixture directory twice: counts identical, second run all duplicates", func(t *testing.T) {
		reset()
		var out bytes.Buffer
		if err := run([]string{"ingest", fixtures}, &out); err != nil {
			t.Fatalf("first run: %v\n%s", err, out.String())
		}
		if !strings.Contains(out.String(), "17 events: 17 new, 0 duplicate") {
			t.Fatalf("first run output: %s", out.String())
		}
		before := count("source_events")
		activities := count("activities")

		out.Reset()
		if err := run([]string{"ingest", fixtures}, &out); err != nil {
			t.Fatalf("second run: %v", err)
		}
		if !strings.Contains(out.String(), "17 events: 0 new, 17 duplicate") {
			t.Fatalf("second run output: %s", out.String())
		}
		if count("source_events") != before || count("activities") != activities || before != 17 {
			t.Fatalf("row counts changed on replay: events %d->%d activities %d->%d", before, count("source_events"), activities, count("activities"))
		}
		if strings.Contains(out.String(), "ghost:ghost") {
			t.Fatal("output leaks credentials")
		}
	})

	t.Run("files are ingested in lexical order and a single file works", func(t *testing.T) {
		reset()
		dir := t.TempDir()
		writeEvent(t, filepath.Join(dir, "b.json"), emailEvent("m-b"))
		writeEvent(t, filepath.Join(dir, "a.json"), emailEvent("m-a"))
		var out bytes.Buffer
		if err := run([]string{"ingest", dir}, &out); err != nil {
			t.Fatal(err)
		}
		var order string
		if err := env.DB.QueryRow(`SELECT string_agg(source_object_id, ',' ORDER BY received_at, id) FROM source_events`).Scan(&order); err != nil {
			t.Fatal(err)
		}
		if order != "m-a,m-b" {
			t.Fatalf("ingestion order = %s, want m-a,m-b (lexical file order)", order)
		}
		if !strings.Contains(out.String(), "2 events: 2 new, 0 duplicate") {
			t.Fatalf("output: %s", out.String())
		}
		out.Reset()
		if err := run([]string{"ingest", filepath.Join(dir, "a.json")}, &out); err != nil || !strings.Contains(out.String(), "1 events: 0 new, 1 duplicate") {
			t.Fatalf("single file: %v %s", err, out.String())
		}
	})

	t.Run("an invalid event stops the run, reports progress and names the file", func(t *testing.T) {
		reset()
		dir := t.TempDir()
		writeEvent(t, filepath.Join(dir, "1_ok.json"), emailEvent("m-ok"))
		writeEvent(t, filepath.Join(dir, "2_bad.json"), `{"source_system":"email","source_object_id":"x","source_event_key":"received","payload":{"kind":"call"}}`)
		writeEvent(t, filepath.Join(dir, "3_never.json"), emailEvent("m-never"))
		var out bytes.Buffer
		err := run([]string{"ingest", dir}, &out)
		if err == nil || !strings.Contains(err.Error(), "2_bad.json") || !strings.Contains(err.Error(), "invalid_event") {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(out.String(), "1 new, 0 duplicate") {
			t.Errorf("progress before the failure not reported: %s", out.String())
		}
		if got := count("source_events"); got != 1 {
			t.Errorf("source_events = %d, want 1 (stopped at the failure)", got)
		}
	})
}

func abs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
