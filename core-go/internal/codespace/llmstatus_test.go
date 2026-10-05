package codespace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

func writeStats(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cache-stats.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLLMStatusSaysReplayingUnlessANewCallIsBeingOrWasJustRecorded(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, "cache-stats.json")
	if st := llmStatusFrom(path, now); st.Activity != "replaying" || st.Text != "Replaying recorded run" {
		t.Fatalf("no stats yet = %+v", st)
	}
	for _, tc := range []struct {
		name, body, want string
	}{
		{"replaying", `{"hits":7,"recorded":3,"recording":false,"last_source":"replay","last_at":"2026-10-04T11:59:59Z"}`, "replaying"},
		{"in flight", `{"hits":7,"recorded":3,"recording":true,"last_source":"replay","last_at":"2026-10-04T11:00:00Z"}`, "recording"},
		{"just recorded", `{"hits":7,"recorded":4,"recording":false,"last_source":"record","last_at":"2026-10-04T11:59:00Z"}`, "recording"},
		{"recorded long ago", `{"hits":7,"recorded":4,"recording":false,"last_source":"record","last_at":"2026-10-04T11:00:00Z"}`, "replaying"},
		{"unreadable", `{not json`, "replaying"},
	} {
		writeStats(t, dir, tc.body)
		st := llmStatusFrom(path, now)
		if st.Activity != tc.want {
			t.Errorf("%s: activity = %s, want %s", tc.name, st.Activity, tc.want)
		}
		if tc.want == "recording" && st.Text != "Recording new call" {
			t.Errorf("%s: text = %s", tc.name, st.Text)
		}
	}
	writeStats(t, dir, `{"hits":7,"recorded":3,"spend_blocked":true,"last_source":"replay"}`)
	if st := llmStatusFrom(path, now); st.Hits != 7 || st.Recorded != 3 || !st.SpendBlocked {
		t.Fatalf("counters = %+v", st)
	}
}

func TestReadLLMStatusIsOnlyForCacheMode(t *testing.T) {
	cfg := testBase()
	cfg.Layout.StateDir = t.TempDir()
	if ReadLLMStatus(cfg, time.Now()) != nil {
		t.Fatal("no cache, no status")
	}
	cfg.LLMMode = "cache"
	writeStats(t, cfg.CacheDir(), `{"hits":1,"recorded":0}`)
	if st := ReadLLMStatus(cfg, time.Now()); st == nil || st.Hits != 1 {
		t.Fatalf("status = %+v", st)
	}
	var _ demorun.Config = cfg
}

func TestCacheDirAndWorkerEnvPointTheCacheUnderTheDemoHome(t *testing.T) {
	cfg := testBase()
	cfg.Layout.StateDir = filepath.Join("home")
	cfg.LLMMode = "cache"
	env := cfg.WorkerEnv()
	if env["GHOST_LLM_MODE"] != "cache" || env["GHOST_LLM_CACHE_DIR"] != filepath.Join("home", "cassettes") {
		t.Fatalf("worker env = %v", env.Names())
	}
	cfg.LLMMode = "live"
	if _, ok := cfg.WorkerEnv()["GHOST_LLM_CACHE_DIR"]; ok {
		t.Fatal("the cache dir is only for cache mode")
	}
}
