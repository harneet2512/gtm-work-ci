package neo4jtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// PersistentOptions describe a long-running local Neo4j for the live demo (HAR-137). It reuses the pinned,
// checksum-verified tarball and the Java launcher of the test harness, but keeps its data under Dir and uses a
// fixed Bolt port and a caller-supplied password, so a restarted demo reconnects to the same graph.
type PersistentOptions struct {
	Dir      string // data, logs, conf and transaction logs live below it and survive Close
	BoltPort int
	Password string // set as the initial password on the first start; the caller keeps it
	CacheDir string // where the distribution is cached; empty uses the harness default (GHOST_NEO4J_CACHE or <repo>/.neo4j-cache)
}

func (o PersistentOptions) validate() error {
	switch {
	case o.Dir == "":
		return errors.New("neo4jtest: persistent Neo4j needs a directory")
	case o.BoltPort < 1 || o.BoltPort > 65535:
		return fmt.Errorf("neo4jtest: bolt port %d is not a TCP port", o.BoltPort)
	case len(o.Password) < 8:
		return errors.New("neo4jtest: persistent Neo4j needs a password of at least 8 characters")
	}
	return nil
}

// initializedMarker is written once the server answered after its first start. Without it a data directory
// is a half-initialized leftover (a crashed first start) and is rebuilt: the graph is a projection of Postgres.
const initializedMarker = "initialized"

// StartPersistent starts (or restarts) the local Neo4j over o.Dir. The error wraps ErrSkip when Java or the
// download is missing, with the harness's explanation of what to install.
func StartPersistent(ctx context.Context, o PersistentOptions) (*Env, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	javaPath, _, err := findJava(ctx)
	if err != nil {
		return nil, err
	}
	cache := o.CacheDir
	if cache == "" {
		cache = cacheDir()
	}
	home, err := Fetcher{CacheDir: cache}.Ensure(ctx)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("neo4jtest: create %s: %w", o.Dir, err)
	}
	marker := filepath.Join(o.Dir, initializedMarker)
	_, markerErr := os.Stat(marker)
	fresh := os.IsNotExist(markerErr)
	if fresh {
		if err := removeAllRetry(filepath.Join(o.Dir, "data")); err != nil {
			return nil, err
		}
	}
	r := runner{javaPath: javaPath, home: home, run: o.Dir}
	if err := r.writeConf(o.BoltPort); err != nil {
		return nil, err
	}
	if fresh {
		if err := r.setInitialPassword(ctx, o.Password); err != nil {
			return nil, err
		}
	}
	env := &Env{URI: fmt.Sprintf("bolt://127.0.0.1:%d", o.BoltPort), User: "neo4j", Password: o.Password, Database: "neo4j"}
	cmd, exited, logs, err := r.console(ctx)
	if err != nil {
		return nil, err
	}
	// exited yields once and is then closed: fan it out so waitReady and the caller (Done) can both see it.
	done := make(chan error, 1)
	ready := make(chan error, 1)
	go func() {
		err := <-exited
		ready <- err
		done <- err
		close(done)
	}()
	env.done = done
	env.stop = func() error {
		kerr := killTree(cmd)
		<-done
		return kerr
	}
	if err := waitReady(ctx, env, startTimeout, ready); err != nil {
		_ = env.stop()
		return nil, fmt.Errorf("%w\n--- neo4j output ---\n%s", err, redact(tail(logs.String(), 4000), o.Password))
	}
	if fresh {
		if err := os.WriteFile(marker, []byte("1\n"), 0o600); err != nil {
			_ = env.stop()
			return nil, fmt.Errorf("neo4jtest: write %s: %w", initializedMarker, err)
		}
	}
	return env, nil
}

func javaExe() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

// WipePersistent deletes the persistent Neo4j directory (the graph half of `demo reset`); stop it first.
func WipePersistent(dir string) error {
	if dir == "" {
		return errors.New("neo4jtest: refusing to wipe an empty directory")
	}
	return removeAllRetry(dir)
}

// FindJavaHome returns a JAVA_HOME the harness accepts (Java 17 or 21): JAVA_HOME, then java on PATH, then a
// JDK under ~/.jdks (the IntelliJ/jdk-downloader location). It lets the demo start Neo4j from a shell whose
// environment predates the JDK install. ok is false when nothing usable exists.
func FindJavaHome(ctx context.Context) (home string, ok bool) {
	if p, _, err := findJava(ctx); err == nil {
		return filepath.Dir(filepath.Dir(p)), true
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	for _, major := range []string{"21", "17"} {
		matches, _ := filepath.Glob(filepath.Join(userHome, ".jdks", "*"+major+"*"))
		for _, m := range matches {
			exe := filepath.Join(m, "bin", javaExe())
			if _, err := os.Stat(exe); err == nil {
				return m, true
			}
		}
	}
	return "", false
}
