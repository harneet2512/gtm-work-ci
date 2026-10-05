package codespace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// recordingWindow is how long after a recorded call the operator section keeps saying "Recording new call".
const recordingWindow = 90 * time.Second

// Header texts of the model-call activity.
const (
	LLMReplayingText = "Replaying recorded run"
	LLMRecordingText = "Recording new call"
)

// LLMStatus is the worker cache's activity as the operator section of the System page shows it (never the audience header).
type LLMStatus struct {
	Mode         string `json:"mode"`     // cache, or the plain mode when the cache is off
	Activity     string `json:"activity"` // replaying | recording | idle
	Text         string `json:"text"`
	Hits         int    `json:"hits"`
	Recorded     int    `json:"recorded"`
	SpendBlocked bool   `json:"spend_blocked,omitempty"`
}

// ReadLLMStatus reads <home>/cassettes/cache-stats.json (written by the worker's cache mode). It returns nil when the
// demo is not in cache mode. A missing file is a cache nothing has used yet: replaying.
func ReadLLMStatus(cfg demorun.Config, now time.Time) *LLMStatus {
	if cfg.LLMMode != "cache" {
		return nil
	}
	return llmStatusFrom(filepath.Join(cfg.CacheDir(), "cache-stats.json"), now)
}

func llmStatusFrom(path string, now time.Time) *LLMStatus {
	st := &LLMStatus{Mode: "cache", Activity: "replaying", Text: LLMReplayingText}
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var doc struct {
		Hits         int    `json:"hits"`
		Recorded     int    `json:"recorded"`
		Recording    bool   `json:"recording"`
		LastSource   string `json:"last_source"`
		LastAt       string `json:"last_at"`
		SpendBlocked bool   `json:"spend_blocked"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return st
	}
	st.Hits, st.Recorded, st.SpendBlocked = doc.Hits, doc.Recorded, doc.SpendBlocked
	at, _ := time.Parse(time.RFC3339Nano, doc.LastAt)
	if doc.Recording || (doc.LastSource == "record" && !at.IsZero() && now.Sub(at) < recordingWindow) {
		st.Activity, st.Text = "recording", LLMRecordingText
	}
	return st
}
