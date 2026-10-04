package abcrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/api"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// replayVerifier accepts a run token at the replay clock, not the wall clock: the orchestrator mints tokens on the
// fixed replay clock (so a replay builds identical requests), and the pull API must judge their expiry on that clock.
type replayVerifier struct {
	signer *runtoken.Signer
	clk    clock.Clock
}

func (v replayVerifier) Verify(token string, _ time.Time) (string, error) {
	return v.signer.Verify(token, v.clk.Now())
}

// CoreServer is the real context-pull API (GET /internal/ctx/{tool}) over the experiment's database: what the worker
// calls while it generates. Nothing else is mounted that a pull needs; ingest is wired because the handler requires it.
type CoreServer struct {
	srv *httptest.Server
	URL string
}

// StartCore serves the pull API on a loopback port.
func StartCore(db *sql.DB, svc *ingest.Service, signer *runtoken.Signer, clk clock.Clock) (*CoreServer, error) {
	pulls, err := corectx.New(db)
	if err != nil {
		return nil, err
	}
	h, err := api.NewHandler(svc, "abc-experiment-token-not-a-secret", nil, api.WithContext(pulls, replayVerifier{signer: signer, clk: clk}))
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := httptest.NewUnstartedServer(h)
	s.Listener = ln
	s.Start()
	return &CoreServer{srv: s, URL: s.URL}, nil
}

// Close stops the server.
func (c *CoreServer) Close() { c.srv.Close() }

// GenerationWorker is the experiment's orchestrator.Worker: strategies come from the real worker; there is no model
// judge (no LLM judge reaches any number) and no revision (the decision is scored on the first draft, before revision).
// The deterministic evals still run in core on every candidate.
type GenerationWorker struct {
	Inner orchestrator.Worker
	calls atomic.Int64
	pulls atomic.Int64
}

// Strategies forwards to the real worker and counts the generation and its context pulls.
func (g *GenerationWorker) Strategies(ctx context.Context, req workerclient.StrategiesRequest) (workerclient.StrategiesResponse, error) {
	n := g.calls.Add(1)
	if dir := os.Getenv("ABC_DUMP_REQUESTS"); dir != "" {
		raw, _ := json.Marshal(req)
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%03d.json", n)), raw, 0o644)
	}
	resp, err := g.Inner.Strategies(ctx, req)
	g.pulls.Add(int64(resp.ToolCalls))
	return resp, err
}

// Judge returns no semantic items: nothing is judged by a model.
func (g *GenerationWorker) Judge(context.Context, workerclient.JudgeRequest) (workerclient.JudgeResponse, error) {
	return workerclient.JudgeResponse{Items: []workerclient.BundleItem{}, Model: "abc-no-semantic-judge"}, nil
}

// Revise declines in the way the orchestrator treats as 'revision unusable, keep the blocking candidate'.
func (g *GenerationWorker) Revise(context.Context, workerclient.ReviseRequest) (workerclient.ReviseResponse, error) {
	return workerclient.ReviseResponse{}, &workerclient.Error{Status: http.StatusUnprocessableEntity, Code: "invalid_strategies",
		Message: "the uplift experiment scores the first draft and does not revise"}
}

// Generations is the number of strategy generations made; ToolCalls the context pulls they made.
func (g *GenerationWorker) Generations() int { return int(g.calls.Load()) }

// ContextPulls is the number of context pulls the generations made.
func (g *GenerationWorker) ContextPulls() int { return int(g.pulls.Load()) }

// WorkerProc is a spawned real worker (worker-py) in record or replay mode.
type WorkerProc struct {
	cmd *exec.Cmd
	URL string
}

// WorkerSpec says how to start the worker.
type WorkerSpec struct {
	Python       string   // interpreter (default: GHOST_WORKER_PYTHON, else python)
	App          string   // uvicorn factory (default ghost_worker.app:create_app; a rehearsal uses a scripted stand-in for the model)
	RepoRoot     string   // repository root (worker-py/ below it)
	Mode         string   // replay | record
	CassetteDir  string   // CASSETTE_DIR
	CoreURL      string   // CORE_URL of the pull API
	ExtraEnv     []string // KEY=VALUE pairs; secrets are passed through, never logged
	StartTimeout time.Duration
	Logf         func(string, ...any)
}

// StartWorker launches uvicorn on a free port and waits for /healthz.
func StartWorker(ctx context.Context, s WorkerSpec) (*WorkerProc, error) {
	if s.Mode != "replay" && s.Mode != "record" {
		return nil, fmt.Errorf("abcrun: worker mode %q (want replay or record)", s.Mode)
	}
	py := s.Python
	if py == "" {
		py = os.Getenv("GHOST_WORKER_PYTHON")
	}
	if py == "" {
		py = "python"
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	app := s.App
	if app == "" {
		app = "ghost_worker.app:create_app"
	}
	cmd := exec.CommandContext(ctx, py, "-m", "uvicorn", app, "--factory", "--host", "127.0.0.1", "--port", fmt.Sprint(port))
	cmd.Dir = filepath.Join(s.RepoRoot, "worker-py")
	cmd.Env = append(os.Environ(), "GHOST_LLM_MODE="+s.Mode, "CORE_URL="+s.CoreURL, "CASSETTE_DIR="+s.CassetteDir, "PYTHONUNBUFFERED=1")
	if s.Mode == "record" {
		cmd.Env = append(cmd.Env, "CASSETTE_REUSE=true") // an interrupted recording resumes without repeating calls
	}
	cmd.Env = append(cmd.Env, s.ExtraEnv...)
	if s.Logf != nil {
		cmd.Stdout, cmd.Stderr = lineWriter{s.Logf}, lineWriter{s.Logf}
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("abcrun: start worker (%s): %w", py, err)
	}
	w := &WorkerProc{cmd: cmd, URL: fmt.Sprintf("http://127.0.0.1:%d", port)}
	timeout := s.StartTimeout
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	if err := waitHealthy(ctx, w.URL, timeout); err != nil {
		w.Stop()
		return nil, err
	}
	return w, nil
}

// Stop kills the worker.
func (w *WorkerProc) Stop() {
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		return
	}
	_ = w.cmd.Process.Kill()
	_ = w.cmd.Wait()
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitHealthy(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/healthz", nil)
		if err == nil {
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("abcrun: the worker never answered /healthz")
}

// lineWriter forwards the worker's output to a logger line by line. The worker never logs request bodies or keys;
// the environment is passed to the child only.
type lineWriter struct{ logf func(string, ...any) }

func (l lineWriter) Write(p []byte) (int, error) {
	l.logf("worker: %s", string(p))
	return len(p), nil
}
