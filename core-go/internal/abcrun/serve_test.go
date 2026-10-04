package abcrun_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/abcrun"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

func TestTheCoreServerAnswersContextPullsWithATokenValidOnTheReplayClockOnly(t *testing.T) {
	b := newBuilder(t)
	ref, err := b.Build(bg, situation("S-1", "discriminating"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := b.OpenRun(bg, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'context_built' WHERE id = $1::uuid`, run); err != nil {
		t.Fatal(err)
	}
	signer, err := runtoken.NewSigner([]byte("0123456789abcdef0123456789abcdef"), orchestrator.RequiredTokenTTL(true)+time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	replay := clock.NewFixed(trigger.Add(time.Minute)) // years before the wall clock
	srv, err := abcrun.StartCore(env.DB, b.Ingest(), signer, replay)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	get := func(token string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/internal/ctx/state", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	token, err := signer.Issue(run, replay.Now())
	if err != nil {
		t.Fatal(err)
	}
	if code, body := get(token); code != http.StatusOK || !strings.Contains(body, "Quote") {
		t.Fatalf("a token minted on the replay clock must pull the state: %d %s", code, body)
	}
	expired, _ := signer.Issue(run, replay.Now().Add(-48*time.Hour))
	if code, _ := get(expired); code != http.StatusUnauthorized {
		t.Fatalf("a token that expired on the replay clock must be refused, got %d", code)
	}
	if code, _ := get("not-a-token"); code != http.StatusUnauthorized {
		t.Fatalf("a bad token must be refused, got %d", code)
	}
}

func TestTheGenerationWorkerForwardsStrategiesAndNeverJudgesOrRevises(t *testing.T) {
	inner := &scriptedWorker{t: t}
	g := &abcrun.GenerationWorker{Inner: inner}
	judged, err := g.Judge(bg, workerclient.JudgeRequest{})
	if err != nil || len(judged.Items) != 0 || judged.Model == "" {
		t.Fatalf("judge must return no items without a model: %+v %v", judged, err)
	}
	_, err = g.Revise(bg, workerclient.ReviseRequest{})
	var we *workerclient.Error
	if !errors.As(err, &we) || we.Code != "invalid_strategies" {
		t.Fatalf("revise must decline in the way the orchestrator keeps the candidate, got %v", err)
	}
	if g.Generations() != 0 || g.ContextPulls() != 0 {
		t.Fatal("nothing was generated yet")
	}
}

func TestStartWorkerRefusesAnUnknownModeBeforeStartingAnything(t *testing.T) {
	_, err := abcrun.StartWorker(context.Background(), abcrun.WorkerSpec{Mode: "live"})
	if err == nil || !strings.Contains(err.Error(), "replay or record") {
		t.Fatalf("err = %v", err)
	}
}

func TestOutputIsPlainJSONTheReportReads(t *testing.T) {
	raw, err := json.Marshal(abcrun.Output{Version: abcrun.ArmsVersion})
	if err != nil || !strings.Contains(string(raw), `"version":"abc_arms.v1"`) {
		t.Fatalf("%s %v", raw, err)
	}
}
