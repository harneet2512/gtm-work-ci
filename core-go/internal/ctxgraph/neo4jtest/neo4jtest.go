// Package neo4jtest provides a Neo4j for integration tests, the way storetest provides Postgres.
//
// Two paths, no Docker locally:
//
//   - NEO4J_TEST_URI set (the CI service container, or any throwaway Neo4j): that server is used,
//     with NEO4J_TEST_USER and NEO4J_TEST_PASSWORD. Tests wipe it, so the URI must be loopback
//     (or NEO4J_TEST_ALLOW_REMOTE=1) and must differ from the dev NEO4J_URI.
//   - Otherwise a pinned Neo4j Community tarball is found or downloaded into a git-ignored cache
//     directory (GHOST_NEO4J_CACHE, default <repo>/.neo4j-cache), its SHA-256 is verified, and it is
//     run as a Java process on a free port with a generated password. It needs Java 17 or 21.
//
// When a prerequisite is missing (no Java, no network) Require skips the test with a message that
// says what is missing. GHOST_REQUIRE_NEO4J=1 turns every skip into a failure (CI uses it).
package neo4jtest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ErrSkip marks a missing prerequisite: the graph tests cannot run here, which is not a defect.
var ErrSkip = errors.New("neo4j graph tests skipped")

func skipf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrSkip, fmt.Sprintf(format, args...))
}

// Env is a started Neo4j.
type Env struct {
	URI      string
	User     string
	Password string
	Database string
	External bool // true when NEO4J_TEST_URI was used (nothing was started)
	stop     func() error
}

// auth is the driver credential: none for a server with auth disabled.
func (e *Env) auth() neo4j.AuthToken {
	if e.User == "" && e.Password == "" {
		return neo4j.NoAuth()
	}
	return neo4j.BasicAuth(e.User, e.Password, "")
}

// Close stops the Neo4j process this package started; it does nothing for an external server.
func (e *Env) Close() error {
	if e == nil || e.stop == nil {
		return nil
	}
	return e.stop()
}

// Start returns a ready Neo4j. The error wraps ErrSkip when a prerequisite is missing.
func Start(ctx context.Context) (*Env, error) {
	if uri := os.Getenv("NEO4J_TEST_URI"); uri != "" {
		return external(ctx, uri)
	}
	return startLocal(ctx)
}

func external(ctx context.Context, uri string) (*Env, error) {
	if err := checkSafeURI(uri, os.Getenv("NEO4J_URI")); err != nil {
		return nil, err
	}
	env := &Env{URI: uri, User: envOr("NEO4J_TEST_USER", "neo4j"), Password: os.Getenv("NEO4J_TEST_PASSWORD"),
		Database: envOr("NEO4J_TEST_DATABASE", "neo4j"), External: true}
	if os.Getenv("NEO4J_TEST_AUTH") == "none" { // a throwaway server started with auth disabled (the CI service container)
		env.User, env.Password = "", ""
	} else if env.Password == "" {
		return nil, errors.New("neo4jtest: NEO4J_TEST_URI is set but NEO4J_TEST_PASSWORD is empty")
	}
	if err := waitReady(ctx, env, 2*time.Minute, nil); err != nil {
		return nil, err
	}
	return env, nil
}

// checkSafeURI refuses servers that could hold real data: tests wipe the whole database.
func checkSafeURI(testURI, devURI string) error {
	if devURI != "" && testURI == devURI {
		return errors.New("neo4jtest: NEO4J_TEST_URI equals NEO4J_URI; refusing to wipe the dev database")
	}
	u, err := url.Parse(testURI)
	if err != nil || u.Host == "" {
		return fmt.Errorf("neo4jtest: cannot parse NEO4J_TEST_URI %q", testURI)
	}
	host := u.Hostname()
	if os.Getenv("NEO4J_TEST_ALLOW_REMOTE") != "1" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return fmt.Errorf("neo4jtest: NEO4J_TEST_URI host %q is not loopback; set NEO4J_TEST_ALLOW_REMOTE=1 if it is a throwaway server", host)
	}
	return nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// waitReady polls until the server answers a query. exited, when non-nil, reports a launcher that died.
func waitReady(ctx context.Context, env *Env, limit time.Duration, exited <-chan error) error {
	deadline := time.Now().Add(limit)
	var last error
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			return fmt.Errorf("neo4jtest: neo4j exited before it was ready: %v", err)
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if last = ping(ctx, env); last == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("neo4jtest: neo4j at %s not ready after %s: %w", env.URI, limit, last)
}

func ping(ctx context.Context, env *Env) error {
	drv, err := neo4j.NewDriverWithContext(env.URI, env.auth())
	if err != nil {
		return err
	}
	defer drv.Close(ctx)
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := drv.VerifyConnectivity(pctx); err != nil {
		return err
	}
	_, err = neo4j.ExecuteQuery(pctx, drv, "RETURN 1", nil, neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(env.Database))
	return err
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("neo4jtest: free port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// cacheDir is the git-ignored directory the distribution is cached in.
func cacheDir() string {
	if d := os.Getenv("GHOST_NEO4J_CACHE"); d != "" {
		return d
	}
	if root := repoRoot(); root != "" {
		return filepath.Join(root, ".neo4j-cache")
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "ghost-neo4j")
}

// repoRoot walks up from the working directory to the directory holding CLAUDE.md.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

var shared struct {
	once sync.Once
	env  *Env
	err  error
}

// Require returns the package's shared Neo4j, starting it on first use. It skips t when a
// prerequisite is missing (unless GHOST_REQUIRE_NEO4J=1) and fails t on any other error.
func Require(t testing.TB) *Env {
	t.Helper()
	ensure()
	if shared.err != nil {
		if errors.Is(shared.err, ErrSkip) && os.Getenv("GHOST_REQUIRE_NEO4J") != "1" {
			t.Skip(shared.err.Error())
		}
		t.Fatalf("start neo4j: %v", shared.err)
	}
	return shared.env
}

func ensure() {
	shared.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		shared.env, shared.err = Start(ctx)
	})
}

// Available reports whether a Neo4j can be started here (starting it if needed). It never fails a test.
func Available() bool {
	ensure()
	return shared.err == nil
}

// Stop stops a Neo4j this package started and reports a skip. Call it after the tests; Main does.
func Stop() {
	if err := shared.env.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "stop neo4j: %v\n", err)
	}
	if shared.err != nil && errors.Is(shared.err, ErrSkip) {
		fmt.Fprintln(os.Stderr, strings.TrimSpace(shared.err.Error()))
	}
}

// Main is a TestMain helper: it runs the tests and always stops a Neo4j this package started.
func Main(m *testing.M) int {
	code := m.Run()
	Stop()
	return code
}
