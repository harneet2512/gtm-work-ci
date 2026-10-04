package abcrun

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

func TestWaitHealthyReturnsWhenTheWorkerAnswersAndFailsWhenItNeverDoes(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ok.Close()
	if err := waitHealthy(context.Background(), ok.URL, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer down.Close()
	if err := waitHealthy(context.Background(), down.URL, 400*time.Millisecond); err == nil {
		t.Fatal("a worker that never reports healthy must fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitHealthy(ctx, down.URL, time.Minute); err == nil {
		t.Fatal("a cancelled wait must stop")
	}
}

func TestLineWriterForwardsWorkerOutputToTheLogger(t *testing.T) {
	var got []string
	w := lineWriter{func(f string, a ...any) { got = append(got, f+" "+a[0].(string)) }}
	if n, err := w.Write([]byte("started")); n != 7 || err != nil || len(got) != 1 || !strings.Contains(got[0], "started") {
		t.Fatalf("%d %v %v", n, err, got)
	}
	if p, err := freePort(); err != nil || p == 0 {
		t.Fatalf("free port %d %v", p, err)
	}
}

func event(at time.Time, id string) crmarena.Event {
	return crmarena.Event{Source: normalize.SourceEvent{SourceObjectID: id, OccurredAt: &at}}
}

func TestMergeByTimeKeepsBothListsInOrderAndPutsBaseFirstOnATie(t *testing.T) {
	t0 := time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC)
	base := []crmarena.Event{event(t0, "b1"), event(t0.Add(2*time.Hour), "b2")}
	syn := []crmarena.Event{event(t0, "s1"), event(t0.Add(time.Hour), "s2"), event(t0.Add(3*time.Hour), "s3")}
	var ids []string
	for _, e := range mergeByTime(base, syn) {
		ids = append(ids, e.Source.SourceObjectID)
	}
	if got := strings.Join(ids, ","); got != "b1,s1,s2,b2,s3" {
		t.Fatalf("merged %s", got)
	}
	if len(mergeByTime(nil, nil)) != 0 {
		t.Fatal("two empty lists merge to nothing")
	}
}

func TestSiblingRelevantKeepsQuotesAndStageChangesOnly(t *testing.T) {
	quote := crmarena.Event{Source: normalize.SourceEvent{SourceEventKey: "created", Payload: json.RawMessage(`{"object_type":"Quote"}`)}}
	stage := crmarena.Event{Source: normalize.SourceEvent{SourceEventKey: "field:StageName:Quote", Payload: json.RawMessage(`{"object_type":"Opportunity"}`)}}
	task := crmarena.Event{Source: normalize.SourceEvent{SourceEventKey: "created", Payload: json.RawMessage(`{"object_type":"Task"}`)}}
	bad := crmarena.Event{Source: normalize.SourceEvent{SourceEventKey: "created", Payload: json.RawMessage(`not json`)}}
	if !siblingRelevant(quote) || !siblingRelevant(stage) || siblingRelevant(task) || siblingRelevant(bad) {
		t.Fatal("only a Quote record or a stage change of another deal is visible")
	}
}

func TestStateHeaderAndTriggerSummaryDescribeWhatTheAgentSaw(t *testing.T) {
	state := `{"account_name":"Orbit","fields":{"stage":{"value":"Quote"},"objections":{"value":[{"text":"Pricing is high","status":"open"}]},"blockers":{"value":[{"text":"Security review","status":"open"}]}}}`
	got := stateHeader([]byte(state))
	for _, want := range []string{"Orbit: stage Quote", "open objection: Pricing is high", "open blocker: Security review"} {
		if !strings.Contains(got, want) {
			t.Errorf("header %q lacks %q", got, want)
		}
	}
	if stateHeader([]byte("{")) != "account state unavailable" || orAccount("") != "Account" || orAccount("X") != "X" {
		t.Fatal("a broken state and a nameless account degrade to plain words")
	}
	at := time.Date(2023, 11, 2, 0, 0, 0, 0, time.UTC)
	mail := normalize.SourceEvent{SourceSystem: "email", SourceObjectID: "m1", SourceEventKey: "received", OccurredAt: &at,
		Payload: json.RawMessage(`{"subject":"Re: proposal","body_text":"Too expensive.","from":{"name":"Dana","email":"d@x.example"}}`)}
	crm := normalize.SourceEvent{SourceSystem: "crm", SourceObjectID: "opp:1", SourceEventKey: "field:StageName:Quote", OccurredAt: &at, Payload: json.RawMessage(`{}`)}
	if s := triggerSummary(Situation{Trigger: Trigger{SourceObjectID: "m1", SourceEventKey: "received"}, Events: []normalize.SourceEvent{mail}}); !strings.Contains(s, "Too expensive.") || !strings.Contains(s, "Dana") {
		t.Fatalf("email summary %q", s)
	}
	if s := triggerSummary(Situation{Trigger: Trigger{SourceObjectID: "opp:1", SourceEventKey: "field:StageName:Quote"}, Events: []normalize.SourceEvent{crm}}); !strings.Contains(s, "StageName:Quote") {
		t.Fatalf("crm summary %q", s)
	}
	if triggerSummary(Situation{}) != "trigger event" {
		t.Fatal("a missing trigger degrades to plain words")
	}
}
