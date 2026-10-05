package demorun

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Layout is where everything the demo writes lives: one git-ignored `.demo` directory under the repository
// root, so `demo reset` is a bounded delete and nothing leaks into tracked files.
type Layout struct {
	Root string // repository root (the checkout the command runs from)
	// StateDir, when set (GHOST_DEMO_DIR), replaces <Root>/.demo. The integration smoke uses it so it never touches
	// the user's real demo data.
	StateDir string
}

// NewLayout returns the layout for a repository root.
func NewLayout(root string) Layout { return Layout{Root: root} }

// Dir is the .demo directory (or GHOST_DEMO_DIR).
func (l Layout) Dir() string {
	if l.StateDir != "" {
		return l.StateDir
	}
	return filepath.Join(l.Root, ".demo")
}

// PIDDir holds one PID file per service.
func (l Layout) PIDDir() string { return filepath.Join(l.Dir(), "pids") }

// LogDir holds one log file per service.
func (l Layout) LogDir() string { return filepath.Join(l.Dir(), "logs") }

// LogFile is the log of one service.
func (l Layout) LogFile(service string) string { return filepath.Join(l.LogDir(), service+".log") }

// PGDir is the persistent Postgres root (data, runtime, binaries).
func (l Layout) PGDir() string { return filepath.Join(l.Dir(), "pg") }

// Neo4jDir is the persistent Neo4j root.
func (l Layout) Neo4jDir() string { return filepath.Join(l.Dir(), "neo4j") }

// BinDir holds the binaries `demo up` builds.
func (l Layout) BinDir() string { return filepath.Join(l.Dir(), "bin") }

// SecretsFile holds the generated API token, run-token secret and Neo4j password (0600, never printed).
func (l Layout) SecretsFile() string { return filepath.Join(l.Dir(), "secrets.env") }

// ManifestFile is the frozen demo manifest JSON `demo seed` writes.
func (l Layout) ManifestFile() string { return filepath.Join(l.Dir(), "manifest.json") }

// ReplayEventsDir holds the replay dataset core's Play reads (GHOST_REPLAY_EVENTS).
func (l Layout) ReplayEventsDir() string { return filepath.Join(l.Dir(), "replay-events") }

// VenvDir is the Python virtual environment of the worker.
func (l Layout) VenvDir() string { return filepath.Join(l.Dir(), "venv") }

// StateFile remembers what the last seed and play produced (ids, not secrets).
func (l Layout) StateFile() string { return filepath.Join(l.Dir(), "state.json") }

// PIDs returns the PID store of this layout.
func (l Layout) PIDs() PIDStore { return PIDStore{Dir: l.PIDDir()} }

// FindRoot walks up from start to the repository root: the directory holding CLAUDE.md and core-go.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("demorun: resolve %s: %w", start, err)
	}
	for {
		_, e1 := os.Stat(filepath.Join(dir, "CLAUDE.md"))
		info, e2 := os.Stat(filepath.Join(dir, "core-go"))
		if e1 == nil && e2 == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("demorun: not inside the gtm-work repository (no CLAUDE.md next to core-go/ above the working directory)")
		}
		dir = parent
	}
}

// MainCheckout returns the main checkout of a worktree path (the directory above .claude/worktrees), or the
// path itself. The git-ignored .env and data/ live only there.
func MainCheckout(root string) string {
	marker := string(filepath.Separator) + filepath.Join(".claude", "worktrees") + string(filepath.Separator)
	if i := strings.Index(root, marker); i >= 0 {
		return root[:i]
	}
	return root
}

// Ports are the fixed loopback ports of the demo services.
type Ports struct{ Core, Worker, Web, Postgres, Bolt int }

// DefaultPorts returns the ports, overridable by GHOST_DEMO_PORT_CORE|WORKER|WEB|POSTGRES|BOLT. A value that is
// not a valid TCP port falls back to the default instead of failing, so a typo cannot half-start the demo.
func DefaultPorts(env Env) Ports {
	pick := func(name string, def int) int {
		if n, err := strconv.Atoi(env[name]); err == nil && n >= 1 && n <= 65535 {
			return n
		}
		return def
	}
	return Ports{
		Core: pick("GHOST_DEMO_PORT_CORE", 8080), Worker: pick("GHOST_DEMO_PORT_WORKER", 8090),
		Web: pick("GHOST_DEMO_PORT_WEB", 3000), Postgres: pick("GHOST_DEMO_PORT_POSTGRES", 15432),
		Bolt: pick("GHOST_DEMO_PORT_BOLT", 17687),
	}
}

var secretNames = []string{"GHOST_API_TOKEN", "GHOST_RUN_TOKEN_SECRET", "NEO4J_PASSWORD"}

// EnsureSecrets returns the demo's generated secrets, creating or completing the 0600 file on first use.
// Existing values are never regenerated: the Neo4j password must match the data directory, and rotating the
// API token would invalidate a running core. Errors never contain a value.
func EnsureSecrets(path string) (Env, error) {
	have, _, err := LoadEnvFile(path)
	if err != nil {
		return nil, err
	}
	changed := false
	for _, name := range secretNames {
		if have[name] != "" {
			continue
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("demorun: generate %s: %w", name, err)
		}
		have[name] = hex.EncodeToString(b)
		changed = true
	}
	if !changed {
		return have, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("demorun: create %s: %w", filepath.Dir(path), err)
	}
	var sb strings.Builder
	for _, k := range have.Names() {
		sb.WriteString(k + "=" + have[k] + "\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		return nil, fmt.Errorf("demorun: write %s: %w", path, err)
	}
	return have, nil
}
