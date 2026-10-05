package slacksurface

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func goodEnv() map[string]string {
	return map[string]string{
		EnvAppToken: "xapp-" + "SECRETAPP", EnvBotToken: "xoxb-" + "SECRETBOT", EnvChannelID: "C123",
		EnvCoreURL: "http://127.0.0.1:8080", EnvAPIToken: "SECRETCORE",
	}
}

func TestLoadConfigNamesMissingVariablesButNeverValues(t *testing.T) {
	for _, name := range []string{EnvAppToken, EnvBotToken, EnvChannelID, EnvCoreURL, EnvAPIToken} {
		m := goodEnv()
		delete(m, name)
		_, err := LoadConfig(env(m))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("missing %s: err = %v", name, err)
		}
		for _, secret := range []string{"SECRETAPP", "SECRETBOT", "SECRETCORE"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaks a secret value: %v", err)
			}
		}
	}
	if _, err := LoadConfig(env(nil)); err == nil {
		t.Fatal("empty environment must be refused")
	}
}

func TestLoadConfigRejectsWrongTokenKindsWithoutEchoing(t *testing.T) {
	m := goodEnv()
	m[EnvAppToken], m[EnvBotToken] = "xoxb-WRONGKIND", "xapp-WRONGKIND"
	_, err := LoadConfig(env(m))
	if err == nil || !strings.Contains(err.Error(), EnvAppToken) || !strings.Contains(err.Error(), EnvBotToken) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "WRONGKIND") {
		t.Fatal("error echoes a token value")
	}
}

func TestSecretsNeverPrint(t *testing.T) {
	cfg, err := LoadConfig(env(goodEnv()))
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{
		fmt.Sprint(cfg), fmt.Sprintf("%v %+v %#v %s", cfg, cfg, cfg, cfg.AppToken), cfg.String(),
		fmt.Sprintf("%v|%#v", cfg.BotToken, cfg.APIToken),
	} {
		for _, secret := range []string{"SECRETAPP", "SECRETBOT", "SECRETCORE"} {
			if strings.Contains(out, secret) {
				t.Fatalf("%q leaks %s", out, secret)
			}
		}
	}
	if b, _ := cfg.AppToken.MarshalText(); strings.Contains(string(b), "SECRET") {
		t.Fatal("MarshalText leaks")
	}
}

func TestCoreHTTPSendsBearerAndMapsStatuses(t *testing.T) {
	status := http.StatusOK
	body := `{}`
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := NewCoreHTTP(srv.URL+"/", "tok", nil)
	ctx := context.Background()
	if _, err := c.GetStrategies(ctx, "run 1"); err != nil || gotAuth != "Bearer tok" {
		t.Fatalf("err=%v auth=%q", err, gotAuth)
	}
	cases := []struct {
		status int
		body   string
		check  func(error) bool
	}{
		{404, `{"error":{"code":"not_found","message":"x"}}`, func(e error) bool { return errors.Is(e, ErrNotFound) }},
		{404, `{"error":{"code":"strategies_not_ready","message":"x"}}`, func(e error) bool { return errors.Is(e, ErrNotReady) }},
		{404, `{"error":{"code":"inference_not_ready","message":"x"}}`, func(e error) bool { return errors.Is(e, ErrNotReady) }},
		{409, `{"error":{"code":"selection_locked","message":"x"}}`, func(e error) bool {
			var ce *ConflictError
			return errors.Is(e, ErrConflict) && errors.As(e, &ce) && ce.Code == CodeSelectionLocked && ce.Decision == nil
		}},
		{409, `{"selected_candidate_id":"c1","send_decision":"send"}`, func(e error) bool {
			var ce *ConflictError
			return errors.As(e, &ce) && ce.Code == CodeAlreadyDecided && ce.Decision != nil && ce.Decision.SelectedCandidateID == "c1"
		}},
		{422, `{"error":{"code":"refused","message":"blocking eval"}}`, func(e error) bool {
			var re *RefusedError
			return errors.As(e, &re) && re.Message == "blocking eval"
		}},
		{500, `{"error":{"code":"internal","message":"boom"}}`, func(e error) bool { return e != nil && strings.Contains(e.Error(), "boom") }},
		{502, `not json`, func(e error) bool { return e != nil && strings.Contains(e.Error(), "502") }},
	}
	for _, tc := range cases {
		status, body = tc.status, tc.body
		if _, err := c.SendRun(ctx, "r", SendRequest{Decision: SendSend}); !tc.check(err) {
			t.Fatalf("status %d %s: err = %v", tc.status, tc.body, err)
		}
	}
	status, body = 200, `not json`
	if _, err := c.GetBIUpdate(ctx, "a"); err == nil {
		t.Fatal("an undecodable answer must be an error")
	}
	srv.Close()
	if _, err := c.GetBIUpdate(ctx, "e"); err == nil {
		t.Fatal("a dead core must be an error")
	}
}

func TestCoreHTTPAllEndpoints(t *testing.T) {
	core := newMemCore()
	srv := httpCore(t, core)
	c := NewCoreHTTP(srv.URL, testToken, nil)
	ctx := context.Background()
	if bi, err := c.GetBIUpdate(ctx, FixtureAccountID); err != nil || len(bi.Claims) == 0 {
		t.Fatalf("bi: %v", err)
	}
	if name, err := c.GetAccountName(ctx, FixtureAccountID); err != nil || name == "" {
		t.Fatalf("name: %v", err)
	}
	if dir, err := c.People(ctx, FixtureAccountID); err != nil || dir[fixtureMarco].Email != "marco@acme.example.test" {
		t.Fatalf("people: %v %v", err, dir)
	}
	rs, err := c.GetStrategies(ctx, FixtureRunID)
	if err != nil || len(rs.StrategySet.Candidates) != 3 || len(rs.EvalBundles) != 3 {
		t.Fatalf("strategies: %v", err)
	}
	if _, err := c.GetStrategyDecision(ctx, FixtureRunID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	req := StrategyDecisionRequest{SelectedCandidateID: fixtureCandB, Surface: SurfaceSlack, ActorLabel: "alex"}
	if _, err := c.RecordStrategyDecision(ctx, FixtureRunID, req); err != nil {
		t.Fatal(err)
	}
	req.SelectedCandidateID = fixtureCandA
	var ce *ConflictError
	if _, err := c.RecordStrategyDecision(ctx, FixtureRunID, req); !errors.As(err, &ce) || ce.Code != CodeSelectionLocked {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.SendRun(ctx, FixtureRunID, SendRequest{Decision: SendSend, Surface: SurfaceSlack, ActorLabel: "alex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetJudgmentInference(ctx, FixtureEpisodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SubmitJudgmentVerdict(ctx, FixtureEpisodeID, VerdictRequest{Note: "n", Surface: SurfaceSlack, ActorLabel: "alex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCoreHTTP(srv.URL, "wrong", nil).GetBIUpdate(ctx, "e"); err == nil {
		t.Fatal("a wrong token must be refused")
	}
}
