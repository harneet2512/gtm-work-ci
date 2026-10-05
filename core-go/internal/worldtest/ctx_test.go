package worldtest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
)

// ctxTools are the pulls of the draft agent; evidence needs a field path, so it appears once per field.
var ctxTools = []struct{ tool, query string }{
	{"state", "limit=20"}, {"people", "limit=20"}, {"commitments", "limit=20"}, {"recent_diffs", "limit=20"},
	{"evidence", "field_path=stage&limit=20"}, {"evidence", "field_path=blockers&limit=20"},
	{"activities", "limit=20"}, {"graph_neighborhood", "limit=20"},
}

func decode(t *testing.T, r reply) corectx.Packet {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("status %d: %s", r.status, r.body)
	}
	var p corectx.Packet
	if err := json.Unmarshal(r.body, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// The run's trigger event is E(k): every tool serves the world up to and including it, and nothing of
// E(k+1)..E5, whatever the model asks. The cutoff is derived from the run on the server.
func TestRunContextNeverReadsPastTheTriggerEvent(t *testing.T) {
	s := newStack(t)
	w := s.world
	for k := 1; k <= 5; k++ {
		token := s.runToken(k)
		want := justAfter(w.At(k))
		later := claimIDs(t, k+1, 5, w)
		for _, c := range ctxTools {
			r := s.pull(token, c.tool, c.query)
			p := decode(t, r)
			what := "ctx " + c.tool + " for the run triggered by E" + itoa(k)
			if p.WorldAsOf == nil || !p.WorldAsOf.Equal(want) {
				t.Errorf("%s: world_as_of = %v, want %s", what, p.WorldAsOf, want)
			}
			body := string(r.body)
			if k < 5 {
				assertNoneOf(t, what, body, markersFrom(k+1)...)
				assertNoneOf(t, what, body, ids(w, k+1, 5)...)
				assertNoneOf(t, what, body, sourceEvents(w, k+1, 5)...)
				assertNoneOf(t, what, body, later...)
				if k+1 <= 4 {
					assertNoneOf(t, what, body, w.SignalID)
				}
			}
		}
	}
}

// What the run reacts to must be there: the trigger event itself and the value it set.
func TestRunContextIncludesTheTriggerEventItself(t *testing.T) {
	s := newStack(t)
	w := s.world
	token := s.runToken(3)

	state := s.pull(token, "state", "field_path=stage")
	if !strings.Contains(string(state.body), worldfixtureStage(3)) {
		t.Fatalf("the stage the trigger event set must be in the state: %s", state.body)
	}
	if strings.Contains(string(state.body), worldfixtureStage(1)) {
		t.Fatalf("the stage the trigger event replaced must not: %s", state.body)
	}
	acts := s.pull(token, "activities", "limit=20")
	var p corectx.Packet
	if err := json.Unmarshal(acts.body, &p); err != nil || len(p.Items) != 3 {
		t.Fatalf("E1..E3 are the world of the E3 run, got %d items: %s", len(p.Items), acts.body)
	}
	if first := string(p.Items[0]); !strings.Contains(first, w.Event(3).ActivityID) || !strings.Contains(first, `"is_trigger":true`) {
		t.Fatalf("the trigger comes first: %s", first)
	}
}

// Evidence reports claim status as it was at the cutoff: the claim the trigger replaced is superseded
// only from E3 on, and its replacement does not exist for the E2 run.
func TestRunContextEvidenceStatusIsThatOfTheCutoff(t *testing.T) {
	s := newStack(t)
	statusOf := func(token string) map[string]string {
		p := decode(t, s.pull(token, "evidence", "field_path=stage&limit=20"))
		out := map[string]string{}
		for _, raw := range p.Items {
			var c struct{ Value, Status string }
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			out[strings.Trim(c.Value, `"`)] = c.Status
		}
		return out
	}
	e2 := statusOf(s.runToken(2))
	if len(e2) != 1 || e2["Discovery"] != "active" {
		t.Fatalf("E2 run: want only the standing Discovery claim, got %v", e2)
	}
	e3 := statusOf(s.runToken(3))
	if len(e3) != 2 || e3["Negotiation"] != "active" || e3["Discovery"] != "superseded" {
		t.Fatalf("E3 run: %v", e3)
	}
	e5 := statusOf(s.runToken(5))
	if len(e5) != 3 || e5["Closed Won"] != "active" || e5["Negotiation"] != "superseded" || e5["Discovery"] != "superseded" {
		t.Fatalf("E5 run: %v", e5)
	}
}

// There is no way to name a time: the endpoint takes field_path and limit only.
func TestRunContextTakesNoWorldTimeFromTheCaller(t *testing.T) {
	s := newStack(t)
	token := s.runToken(2)
	for _, q := range []string{"world_as_of=2030-01-01T00:00:00Z", "as_of=2030-01-01T00:00:00Z", "before=2030-01-01T00:00:00Z"} {
		if r := s.pull(token, "state", q); r.status != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", q, r.status)
		}
	}
	p := decode(t, s.pull(token, "state", ""))
	if p.WorldAsOf == nil || !p.WorldAsOf.Equal(justAfter(s.world.At(2))) {
		t.Fatalf("world_as_of = %v", p.WorldAsOf)
	}
}

// A run whose trigger activity cannot be found has no place in world time: it is refused, never served
// current data (which would show E4 and E5 to a run reacting to E2).
func TestRunWhoseTriggerCannotBePlacedIsRefusedNotServedCurrentData(t *testing.T) {
	s := newStack(t)
	runID := s.world.NewRun(t, 2)
	if _, err := env.DB.Exec(`UPDATE agent_runs SET trigger_activity_ids = ARRAY['99999999-9999-4999-8999-999999999999']::uuid[] WHERE id = $1::uuid`, runID); err != nil {
		t.Fatal(err)
	}
	tok, err := s.signer.Issue(runID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"state", "activities", "graph_neighborhood"} {
		if r := s.pull(tok, tool, ""); r.status != http.StatusForbidden {
			t.Errorf("%s: %d %s, want 403", tool, r.status, r.body)
		}
	}
}

// The cutoff is logged with the pull, so a replay can prove what each pull saw.
func TestRunContextLogsTheCutoffInTheAccessLog(t *testing.T) {
	s := newStack(t)
	p := decode(t, s.pull(s.runToken(3), "state", "limit=3"))
	var got string
	if err := env.DB.QueryRow(`SELECT args ->> 'world_as_of' FROM context_access_log WHERE id = $1`, p.AccessID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if want := justAfter(s.world.At(3)).UTC().Format(time.RFC3339Nano); got != want {
		t.Fatalf("logged world_as_of = %q, want %q", got, want)
	}
}

func worldfixtureStage(n int) string {
	return map[int]string{1: "Discovery", 3: "Negotiation", 5: "Closed Won"}[n]
}
