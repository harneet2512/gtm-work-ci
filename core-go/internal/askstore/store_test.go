package askstore_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ask"
	"github.com/harneet2512/gtm-work/core-go/internal/askstore"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) { os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e })) }

var bg = context.Background()

func pg(t *testing.T) ask.Store {
	t.Helper()
	s, err := askstore.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The Postgres store and the in-memory store must behave the same: the service is tested against one, run against the other.
func stores(t *testing.T) map[string]ask.Store {
	return map[string]ask.Store{"postgres": pg(t), "memory": ask.NewMemoryStore()}
}

func at(min int) time.Time { return time.Date(2026, 10, 6, 12, min, 0, 0, time.UTC) }

func TestTurnsComeBackOldestFirstLatestOnlyAndPerThread(t *testing.T) {
	for name, s := range stores(t) {
		ref := "C1:" + name
		var turns []ask.Turn
		for i := 0; i < 6; i++ {
			role := ask.RoleUser
			if i%2 == 1 {
				role = ask.RoleCliff
			}
			turns = append(turns, ask.Turn{Role: role, Text: string(rune('a' + i)), At: at(i)})
		}
		if err := s.AppendTurns(bg, ref, turns...); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := s.AppendTurns(bg, ref+"-other", ask.Turn{Role: ask.RoleUser, Text: "elsewhere", At: at(9)}); err != nil {
			t.Fatal(err)
		}
		got, err := s.Turns(bg, ref, 4)
		if err != nil || len(got) != 4 || got[0].Text != "c" || got[3].Text != "f" || got[3].Role != ask.RoleCliff || !got[3].At.Equal(at(5)) {
			t.Errorf("%s: turns = %+v %v", name, got, err)
		}
		if none, _ := s.Turns(bg, "C1:nothing", 8); len(none) != 0 {
			t.Errorf("%s: an unknown thread has no turns: %+v", name, none)
		}
	}
}

func TestOnePausedTaskPerConversationClearedByNil(t *testing.T) {
	for name, s := range stores(t) {
		ref := "C2:" + name
		if p, err := s.Pending(bg, ref); err != nil || p != nil {
			t.Fatalf("%s: %+v %v", name, p, err)
		}
		first := ask.Pending{Kind: "play_next", Question: "play it and tell me", ChannelKind: "thread", At: at(1)}
		second := ask.Pending{Kind: "demo_status", Question: "status then summarise", ChannelKind: "dm", At: at(2)}
		for _, p := range []ask.Pending{first, second} {
			p := p
			if err := s.SetPending(bg, ref, &p); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		got, err := s.Pending(bg, ref)
		if err != nil || got == nil || got.Kind != "demo_status" || got.Question != second.Question || got.ChannelKind != "dm" || !got.At.Equal(at(2)) {
			t.Errorf("%s: pending = %+v %v", name, got, err)
		}
		if err := s.SetPending(bg, ref, nil); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Pending(bg, ref); got != nil {
			t.Errorf("%s: still pending after clear: %+v", name, got)
		}
	}
}

func TestATraceRoundTripsAndAnUnknownOneIsNotFound(t *testing.T) {
	for name, s := range stores(t) {
		cost := 0.0042
		in := ask.Trace{ID: "tr_" + name, ThreadRef: "C3:1", Question: "q", AnswerMarkdown: "a", CreatedAt: at(3), Model: "m",
			Steps:    []ask.TraceStep{{N: 1, Tool: "account_state", Args: map[string]any{"account": "MedTech"}, OK: true, Output: map[string]any{"stage": "Negotiation"}}},
			TokensIn: 100, TokensOut: 20, CostUSD: &cost, DurationMS: 900, TimedOut: true}
		if err := s.SaveTrace(bg, in); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out, err := s.Trace(bg, in.ID)
		if err != nil || out.ID != in.ID || out.Question != "q" || len(out.Steps) != 1 || out.Steps[0].Args["account"] != "MedTech" ||
			out.TokensIn != 100 || out.CostUSD == nil || *out.CostUSD != cost || !out.TimedOut || !out.CreatedAt.Equal(at(3)) {
			t.Errorf("%s: trace = %+v %v", name, out, err)
		}
		if _, err := s.Trace(bg, "tr_missing"); !errors.Is(err, ask.ErrNotFound) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAStoreNeedsADatabase(t *testing.T) {
	if _, err := askstore.New(nil); err == nil {
		t.Error("a nil database must be refused")
	}
}
