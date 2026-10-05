package demomine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

func fakeWorker(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	// Every extraction is a cassette miss, as the replay worker answers one.
	mux.HandleFunc("/v1/extract", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"cassette_not_found","message":"no cassette"}}`))
	})
	mux.HandleFunc("/replay-stats", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"cassettes":3,"exact_hits":2,"approximate_hits":1}`))
	})
	return httptest.NewServer(mux)
}

func TestReplayExtractorCountsAMissAndAnswersWithNoClaims(t *testing.T) {
	srv := fakeWorker(t)
	defer srv.Close()
	ex, err := NewReplayExtractor(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ex.Extract(context.Background(), claims.ExtractRequest{Text: "hello"})
	if err != nil || len(resp.Claims) != 0 {
		t.Fatalf("a miss must yield no claims and no error: %+v %v", resp, err)
	}
	st, err := ex.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Calls != 1 || st.Misses != 1 || st.ExactHits != 2 || st.ApproximateHits != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestReplayExtractorFailsWhenTheWorkerIsUnreachable(t *testing.T) {
	srv := fakeWorker(t)
	url := srv.URL
	srv.Close()
	ex, err := NewReplayExtractor(url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Extract(context.Background(), claims.ExtractRequest{Text: "hello"}); err == nil {
		t.Fatal("an unreachable worker is an error, not a miss")
	}
	if _, err := NewReplayExtractor("ftp://nope"); err == nil {
		t.Fatal("a non-http URL must be refused")
	}
}

// approxWorker answers every extraction with no claims and counts the ones whose object id starts with "approx"
// as approximate hits, as the replay worker's second tier does.
func approxWorker(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	approx := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/extract", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Activity struct {
				SourceObjectID string `json:"source_object_id"`
			} `json:"activity"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.HasPrefix(body.Activity.SourceObjectID, "approx") {
			mu.Lock()
			approx++
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"claims":[],"model":"replay","extractor_version":"extract-v4","dropped":0}`))
	})
	mux.HandleFunc("/replay-stats", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"cassettes":3,"exact_hits":0,"approximate_hits":%d}`, approx)
	})
	return httptest.NewServer(mux)
}

func TestReplayExtractorRecordsWhichEventsUsedApproximateHits(t *testing.T) {
	srv := approxWorker(t)
	defer srv.Close()
	ex, err := NewReplayExtractor(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2023, 5, 6, 7, 8, 9, 0, time.UTC)
	for _, obj := range []string{"exact-1", "approx-2", "exact-3", "approx-4"} {
		act := claims.ActivityInput{SourceSystem: "email", SourceObjectID: obj, Type: "EmailSent", OccurredAt: at}
		if _, err := ex.Extract(context.Background(), claims.ExtractRequest{Activity: act, Text: "hello"}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := ex.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.ApproximateEvents) != 2 || st.ApproximateEvents[0].SourceObjectID != "approx-2" || st.ApproximateEvents[1].SourceObjectID != "approx-4" {
		t.Fatalf("the report must list the two events served approximately, in replay order: %+v", st.ApproximateEvents)
	}
	if e := st.ApproximateEvents[0]; e.SourceSystem != "email" || e.ActivityType != "EmailSent" || e.OccurredAt != "2023-05-06T07:08:09Z" {
		t.Fatalf("the event identity is incomplete: %+v", e)
	}
	if st.ApproximateHits != 2 || st.ExactHits != 0 {
		t.Fatalf("counters %+v", st)
	}
}

func TestReplayExtractorWithNoApproximateHitsListsNone(t *testing.T) {
	srv := fakeWorker(t)
	defer srv.Close()
	ex, _ := NewReplayExtractor(srv.URL)
	if _, err := ex.Extract(context.Background(), claims.ExtractRequest{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	st, err := ex.Stats(context.Background())
	if err != nil || st.ApproximateEvents == nil || len(st.ApproximateEvents) != 0 {
		t.Fatalf("an empty list (never null) is expected: %+v %v", st, err)
	}
}
