package demorun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeCoreServer(t *testing.T, handler http.HandlerFunc) CoreClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return CoreClient{Base: srv.URL, Token: "tok-123"}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestCoreClientSendsTheBearerTokenAndDecodesInvisibility(t *testing.T) {
	c := fakeCoreServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			writeJSON(w, 401, map[string]any{"error": map[string]any{"code": "unauthorized", "message": "no"}})
			return
		}
		if r.URL.Path != "/replay/manifests/m-1/invisibility" {
			t.Errorf("path = %s", r.URL.Path)
		}
		writeJSON(w, 200, map[string]any{"manifest_id": "m-1", "held_out_event_id": "e-1", "status": "withheld", "checked": []string{"postgres", "neo4j"}, "leaks": []any{}})
	})
	inv, err := c.Invisibility(context.Background(), "m-1")
	if err != nil || inv.Status != "withheld" || len(inv.Checked) != 2 || len(inv.Leaks) != 0 {
		t.Fatalf("Invisibility = %+v, %v", inv, err)
	}
}

func TestCoreClientTurnsErrorEnvelopesIntoAPIErrors(t *testing.T) {
	c := fakeCoreServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 503, map[string]any{"error": map[string]any{"code": "graph_unavailable", "message": "neo4j cannot be checked"}})
	})
	_, err := c.Invisibility(context.Background(), "m-1")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 503 || ae.Code != "graph_unavailable" || !strings.Contains(err.Error(), "neo4j cannot be checked") {
		t.Fatalf("want an APIError with the envelope, got %v", err)
	}
}

func TestCoreClientPlayMapsSuccessAndAlreadyReleased(t *testing.T) {
	calls := 0
	c := fakeCoreServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Method != http.MethodPost || r.URL.Path != "/replay/play" || body["manifest_id"] != "m-1" {
			t.Errorf("bad request %s %s %v", r.Method, r.URL.Path, body)
		}
		if calls == 1 {
			writeJSON(w, 200, map[string]any{
				"account_change":               map[string]any{"id": "ac-1", "account_id": "acct-1", "material_change": true},
				"business_intelligence_update": map[string]any{"id": "bi-1", "summary": "Budget approval moved"},
				"replay_world":                 map[string]any{},
			})
			return
		}
		writeJSON(w, 409, map[string]any{"error": map[string]any{"code": "already_released", "message": "done"}})
	})
	out, err := c.Play(context.Background(), "m-1")
	if err != nil || out.AccountChangeID != "ac-1" || out.BIUpdateID != "bi-1" || out.BISummary != "Budget approval moved" || out.AlreadyReleased {
		t.Fatalf("first play = %+v, %v", out, err)
	}
	out, err = c.Play(context.Background(), "m-1")
	if err != nil || !out.AlreadyReleased {
		t.Fatalf("a second play is not a failure, it is already released: %+v, %v", out, err)
	}
}

func TestCoreClientPlayWithANonMaterialChangeHasNoBIUpdate(t *testing.T) {
	c := fakeCoreServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"account_change": map[string]any{"id": "ac-2"}, "business_intelligence_update": nil, "replay_world": map[string]any{}})
	})
	out, err := c.Play(context.Background(), "m-1")
	if err != nil || out.BIUpdateID != "" || out.AccountChangeID != "ac-2" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestCoreClientRunMapsGenerationPhase(t *testing.T) {
	c := fakeCoreServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/runs/run-2":
			writeJSON(w, 200, map[string]any{"id": "run-2", "status": "running", "generation": map[string]any{"phase": "published", "attempt": 1, "reason": nil, "strategy_set_id": "ss-1", "decision_episode_id": "ep-1"}})
		case "/runs/run-1":
			writeJSON(w, 200, map[string]any{"id": "run-1", "status": "running"})
		case "/runs/run-3":
			writeJSON(w, 200, map[string]any{"id": "run-3", "status": "running", "generation": map[string]any{"phase": "paused", "attempt": 2, "reason": "rate limited", "strategy_set_id": nil, "decision_episode_id": nil}})
		default:
			t.Errorf("request = %s", r.URL.String())
		}
	})
	r2, err := c.Run(context.Background(), "run-2")
	if err != nil || r2.ID != "run-2" || r2.Phase != "published" || r2.StrategySetID != "ss-1" || r2.EpisodeID != "ep-1" {
		t.Fatalf("run-2 = %+v, %v", r2, err)
	}
	r1, err := c.Run(context.Background(), "run-1")
	if err != nil || r1.Phase != "" {
		t.Fatalf("a run without generation has no phase: %+v %v", r1, err)
	}
	r3, err := c.Run(context.Background(), "run-3")
	if err != nil || r3.Phase != "paused" || r3.Reason != "rate limited" || r3.StrategySetID != "" {
		t.Fatalf("run-3 = %+v, %v", r3, err)
	}
}

func TestCoreClientSurfaceMessage(t *testing.T) {
	c := fakeCoreServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/surface-messages/bi-1/slack/bi":
			writeJSON(w, 200, map[string]any{"subject_id": "bi-1", "surface": "slack", "kind": "bi", "channel": "C1", "ts": "1696420000.000100"})
		case "/surface-messages/ep-1/slack/chooser":
			writeJSON(w, 200, map[string]any{"channel": "C1", "ts": nil}) // reserved, not posted
		default:
			writeJSON(w, 404, map[string]any{"error": map[string]any{"code": "not_found", "message": "none"}})
		}
	})
	if ts, err := c.SurfaceMessageTS(context.Background(), "bi-1", "slack", "bi"); err != nil || ts != "1696420000.000100" {
		t.Fatalf("posted: %q %v", ts, err)
	}
	if ts, err := c.SurfaceMessageTS(context.Background(), "ep-1", "slack", "chooser"); err != nil || ts != "" {
		t.Fatalf("a reservation has no ts yet: %q %v", ts, err)
	}
	if ts, err := c.SurfaceMessageTS(context.Background(), "x", "slack", "judgment"); err != nil || ts != "" {
		t.Fatalf("a 404 means nothing posted, not an error: %q %v", ts, err)
	}
}

func TestPageExistsTreatsOnly2xxAsPresent(t *testing.T) {
	c := fakeCoreServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/runs/abc/evals" {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(200)
	})
	if !PageExists(context.Background(), c.Base+"/replay") {
		t.Fatal("200 is present")
	}
	if PageExists(context.Background(), c.Base+"/runs/abc/evals") {
		t.Fatal("404 is absent")
	}
	if PageExists(context.Background(), "http://127.0.0.1:1/x") {
		t.Fatal("an unreachable page is absent")
	}
}
