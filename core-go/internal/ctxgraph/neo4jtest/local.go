package neo4jtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const startTimeout = 3 * time.Minute

// startLocal runs the pinned Community distribution as a Java process.
func startLocal(ctx context.Context) (*Env, error) {
	reapStale() // servers left behind by tests that were killed before Close
	javaPath, _, err := findJava(ctx)
	if err != nil {
		return nil, err
	}
	home, err := Fetcher{CacheDir: cacheDir()}.Ensure(ctx)
	if err != nil {
		return nil, err
	}
	boltPort, err := freePort()
	if err != nil {
		return nil, err
	}
	pw, err := randomPassword()
	if err != nil {
		return nil, err
	}
	run, err := os.MkdirTemp("", runDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("neo4jtest: runtime dir: %w", err)
	}
	r := runner{javaPath: javaPath, home: home, run: run}
	if err := r.writeConf(boltPort); err != nil {
		_ = os.RemoveAll(run)
		return nil, err
	}
	if err := r.setInitialPassword(ctx, pw); err != nil {
		_ = os.RemoveAll(run)
		return nil, err
	}
	env := &Env{URI: fmt.Sprintf("bolt://127.0.0.1:%d", boltPort), User: "neo4j", Password: pw, Database: "neo4j"}
	cmd, exited, logs, err := r.console(ctx)
	if err != nil {
		_ = os.RemoveAll(run)
		return nil, err
	}
	env.stop = func() error {
		kerr := killTree(cmd)
		<-exited
		rerr := removeAllRetry(run)
		if kerr != nil {
			return kerr
		}
		return rerr
	}
	if err := waitReady(ctx, env, startTimeout, exited); err != nil {
		_ = env.stop()
		return nil, fmt.Errorf("%w\n--- neo4j output ---\n%s", err, tail(logs.String(), 4000))
	}
	return env, nil
}

type runner struct{ javaPath, home, run string }

func (r runner) confDir() string { return filepath.Join(r.run, "conf") }

func slash(p string) string { return filepath.ToSlash(p) }

// writeConf writes neo4j.conf: the distribution's JVM options (minus AlwaysPreTouch, which makes the
// start slow) plus a loopback-only Bolt listener, auth on, and every writable directory under run.
func (r runner) writeConf(boltPort int) error {
	if err := os.MkdirAll(r.confDir(), 0o755); err != nil {
		return fmt.Errorf("neo4jtest: conf dir: %w", err)
	}
	base, err := os.ReadFile(filepath.Join(r.home, "conf", "neo4j.conf"))
	if err != nil {
		return fmt.Errorf("neo4jtest: read distribution conf: %w", err)
	}
	var kept []string
	for _, line := range strings.Split(string(base), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.Contains(t, "AlwaysPreTouch") ||
			strings.HasPrefix(t, "server.http") || strings.HasPrefix(t, "server.bolt") || strings.HasPrefix(t, "server.directories") ||
			strings.HasPrefix(t, "db.tx_log.rotation") {
			continue
		}
		kept = append(kept, t)
	}
	kept = append(kept,
		"server.default_listen_address=127.0.0.1",
		fmt.Sprintf("server.bolt.listen_address=127.0.0.1:%d", boltPort),
		"server.bolt.enabled=true",
		"server.http.enabled=false",
		"server.https.enabled=false",
		"dbms.security.auth_enabled=true",
		"server.directories.data="+slash(filepath.Join(r.run, "data")),
		"server.directories.logs="+slash(filepath.Join(r.run, "logs")),
		"server.directories.run="+slash(filepath.Join(r.run, "run")),
		"server.directories.transaction.logs.root="+slash(filepath.Join(r.run, "tx")),
		"server.directories.import="+slash(filepath.Join(r.run, "import")),
		"server.memory.heap.initial_size=256m",
		"server.memory.heap.max_size=512m",
		"server.memory.pagecache.size=128m",
		"db.tx_log.rotation.retention_policy=keep_none",
	)
	return os.WriteFile(filepath.Join(r.confDir(), "neo4j.conf"), []byte(strings.Join(kept, "\n")+"\n"), 0o600)
}

func (r runner) env() []string {
	javaHome := filepath.Dir(filepath.Dir(r.javaPath))
	return append(os.Environ(), "NEO4J_HOME="+r.home, "NEO4J_CONF="+r.confDir(), "JAVA_HOME="+javaHome)
}

func (r runner) command(ctx context.Context, bootClass string, args ...string) *exec.Cmd {
	full := []string{"-Xmx128m", "-classpath", filepath.Join(r.home, "lib", "*"),
		"-Dapp.name=neo4j", "-Dapp.repo=" + filepath.Join(r.home, "lib"), "-Dapp.home=" + r.home, "-Dbasedir=" + r.home,
		bootClass}
	cmd := exec.CommandContext(ctx, r.javaPath, append(full, args...)...)
	cmd.Env = r.env()
	cmd.Dir = r.home
	return cmd
}

func (r runner) setInitialPassword(ctx context.Context, pw string) error {
	cmd := r.command(ctx, "org.neo4j.server.startup.Neo4jAdminBoot", "dbms", "set-initial-password", pw)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("neo4jtest: set initial password: %w\n%s", err, redact(string(out), pw))
	}
	return nil
}

// console starts the server in the foreground of a child process. exited receives the exit error once.
func (r runner) console(_ context.Context) (*exec.Cmd, <-chan error, *syncBuffer, error) {
	// Not tied to ctx: the process outlives the Start call and is stopped by Env.Close.
	cmd := r.command(context.Background(), "org.neo4j.server.startup.Neo4jBoot", "console")
	var logs syncBuffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	prepare(cmd)
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("neo4jtest: start neo4j: %w", err)
	}
	if err := afterStart(cmd); err != nil {
		_ = killTree(cmd)
		_ = cmd.Wait()
		return nil, nil, nil, err
	}
	if err := writeOwner(r.run, cmd.Process.Pid); err != nil {
		_ = killTree(cmd)
		_ = cmd.Wait()
		return nil, nil, nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait(); close(exited) }()
	return cmd, exited, &logs, nil
}

func randomPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("neo4jtest: generate password: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func redact(s, secret string) string { return strings.ReplaceAll(s, secret, "***") }

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// removeAllRetry removes dir, retrying briefly: on Windows the JVM releases its files a moment after it dies.
func removeAllRetry(dir string) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("neo4jtest: remove %s: %w", dir, err)
}
