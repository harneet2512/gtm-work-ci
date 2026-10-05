package neo4jtest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

func TestPersistentOptionsAreValidated(t *testing.T) {
	for _, o := range []PersistentOptions{
		{Dir: "", BoltPort: 17687, Password: "longenough"},
		{Dir: t.TempDir(), BoltPort: 0, Password: "longenough"},
		{Dir: t.TempDir(), BoltPort: 17687, Password: "short"},
	} {
		if _, err := StartPersistent(context.Background(), o); err == nil {
			t.Errorf("StartPersistent(%+v) should fail before touching anything", o)
		}
	}
}

func TestWipePersistentRefusesEmptyDirAndRemovesAFullOne(t *testing.T) {
	if err := WipePersistent(""); err == nil {
		t.Fatal("an empty dir must never wipe the working directory")
	}
	d := filepath.Join(t.TempDir(), "neo")
	if err := os.MkdirAll(filepath.Join(d, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WipePersistent(d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d); !os.IsNotExist(err) {
		t.Fatalf("directory survived: %v", err)
	}
}

func TestFindJavaHomeFallsBackToTheJdksDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("HOME", home)        // and on Unix
	t.Setenv("JAVA_HOME", "")
	t.Setenv("PATH", "")
	jdk := filepath.Join(home, ".jdks", "jdk-21.0.1")
	if err := os.MkdirAll(filepath.Join(jdk, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jdk, "bin", javaExe()), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := FindJavaHome(context.Background())
	if !ok || got != jdk {
		t.Fatalf("FindJavaHome = %q, %v; want %q", got, ok, jdk)
	}
	if err := os.RemoveAll(filepath.Join(home, ".jdks")); err != nil {
		t.Fatal(err)
	}
	if _, ok := FindJavaHome(context.Background()); ok {
		t.Fatal("no JDK anywhere must report ok=false")
	}
}

// TestPersistentGraphSurvivesARestart starts the real pinned Neo4j twice over one directory and checks that a
// node written before the restart is there after it. It skips, like the other graph tests, when Java or the
// distribution is unavailable.
func TestPersistentGraphSurvivesARestart(t *testing.T) {
	if testing.Short() {
		t.Skip("starts Neo4j twice")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	o := PersistentOptions{Dir: filepath.Join(t.TempDir(), "neo4j"), BoltPort: port, Password: "demo-pass-1234"}

	first, err := StartPersistent(ctx, o)
	if err != nil {
		if errors.Is(err, ErrSkip) { // a restart test of the demo launcher: a missing JDK or download is a skip, not a CI failure
			t.Skip(err.Error())
		}
		t.Fatalf("first start: %v", err)
	}
	run(t, ctx, first, "CREATE (:DemoKeep {v: 'survives'})")
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := StartPersistent(ctx, o)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer second.Close()
	res, err := neo4j.ExecuteQuery(ctx, driverFor(t, second), "MATCH (n:DemoKeep) RETURN n.v AS v", nil,
		neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(second.Database))
	if err != nil || len(res.Records) != 1 {
		t.Fatalf("the node did not survive the restart: %v, %d records", err, len(res.Records))
	}
	if v, _ := res.Records[0].Get("v"); v != "survives" {
		t.Fatalf("v = %v", v)
	}
}

func driverFor(t *testing.T, e *Env) neo4j.DriverWithContext {
	t.Helper()
	d, err := neo4j.NewDriverWithContext(e.URI, e.auth())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	return d
}

func run(t *testing.T, ctx context.Context, e *Env, q string) {
	t.Helper()
	if _, err := neo4j.ExecuteQuery(ctx, driverFor(t, e), q, nil, neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(e.Database)); err != nil {
		t.Fatalf("query: %v", err)
	}
}
