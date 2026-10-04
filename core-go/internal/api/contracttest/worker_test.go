package contracttest

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWorkerClientTalksToTheRealCore runs worker-py/tests/test_core_live.py: the real Python
// CoreContextClient and draft agent against this Go core over HTTP, on the ingested CRMArena sample.
// It needs a Python with the worker installed (pip install -e worker-py[test]); set
// GHOST_WORKER_PYTHON to that interpreter (CI does). No model is called: the agent's turns are scripted.
func TestWorkerClientTalksToTheRealCore(t *testing.T) {
	python := os.Getenv("GHOST_WORKER_PYTHON")
	if python == "" {
		t.Skip("set GHOST_WORKER_PYTHON to a Python with ghost_worker installed to run the cross-language test")
	}
	s := newStack(t)
	runID, token := s.token(s.world.AccountA)
	trigger := scalar(t, `SELECT trigger_activity_ids[1]::text FROM agent_runs WHERE id = $1::uuid`, runID)

	workerDir, err := filepath.Abs("../../../../worker-py")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-m", "pytest", "tests/test_core_live.py", "-p", "no:cacheprovider", "-rs")
	cmd.Dir = workerDir
	// The token travels in the environment, not argv, so it never shows in a process listing.
	cmd.Env = append(os.Environ(),
		"GHOST_CORE_URL="+s.srv.URL, "GHOST_RUN_TOKEN="+token, "GHOST_RUN_ID="+runID,
		"GHOST_ACCOUNT_ID="+s.world.AccountA, "GHOST_OTHER_ACCOUNT_ID="+s.world.AccountB, "GHOST_TRIGGER_ACTIVITY_ID="+trigger)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("worker-py live test failed: %v\n%s", err, out.String())
	}
	text := out.String()
	if strings.Contains(text, "skipped") || !strings.Contains(text, "passed") {
		t.Fatalf("the live worker tests did not all run:\n%s", text)
	}
	if strings.Contains(s.logs.String(), token) {
		t.Fatal("the core logged a run token")
	}
	pulls := scalar(t, `SELECT count(*)::text FROM context_access_log WHERE agent_run_id = $1::uuid`, runID)
	if pulls == "0" {
		t.Fatal("the worker's pulls were not logged in context_access_log")
	}
}
