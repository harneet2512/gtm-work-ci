package codespace

import (
	"path/filepath"
	"strings"
)

// DefaultWindowsHome is where the laptop demo keeps all of its state, outside the repository.
const DefaultWindowsHome = `D:\ghost-demo`

// DefaultModel is the runtime model of the demo when neither the environment nor .env names one.
const DefaultModel = "openrouter/qwen/qwen3.8-flash"

// DemoHome resolves the directory that holds everything the demo writes (Postgres, Neo4j and its pinned tarball,
// logs, PID files, per-case manifests, the data copy): GHOST_DEMO_HOME, else GHOST_DEMO_DIR (the runner's own
// override), else D:\ghost-demo on Windows, else "" (the runner's repo-local .demo). It is never inside the repo
// unless the caller points it there.
func DemoHome(env map[string]string, goos string) string {
	if v := strings.TrimSpace(env["GHOST_DEMO_HOME"]); v != "" {
		return v
	}
	if v := strings.TrimSpace(env["GHOST_DEMO_DIR"]); v != "" {
		return v
	}
	if goos == "windows" {
		return DefaultWindowsHome
	}
	return ""
}

// NeoCacheDir is where the pinned Neo4j tarball is cached: under the demo home, so the tarball is not re-downloaded
// and does not live in the repository.
func NeoCacheDir(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, "cache", "neo4j")
}
