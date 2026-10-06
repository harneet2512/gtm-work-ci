package embedded

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestLaunchPersistentKeepsItsDataAcrossARestartAndResetWipesIt(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Postgres twice")
	}
	root := t.TempDir()
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	opts := PersistentOptions{Dir: root, Port: port}

	dsn, stop, err := LaunchPersistent(opts)
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	execSQL(t, dsn, `CREATE TABLE demo_keep (v text); INSERT INTO demo_keep VALUES ('survives')`)
	if err := stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "data", "PG_VERSION")); err != nil {
		t.Fatalf("the data directory must outlive stop: %v", err)
	}

	dsn, stop, err = LaunchPersistent(opts)
	if err != nil {
		t.Fatalf("second launch: %v", err)
	}
	var v string
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT v FROM demo_keep`).Scan(&v); err != nil || v != "survives" {
		t.Fatalf("data did not survive a restart: %q, %v", v, err)
	}
	_ = db.Close()
	if err := stop(); err != nil {
		t.Fatalf("second stop: %v", err)
	}

	if err := WipePersistent(opts); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "data")); !os.IsNotExist(err) {
		t.Fatalf("wipe must remove the data directory: %v", err)
	}
}

func TestLaunchPersistentRejectsBadOptions(t *testing.T) {
	for _, o := range []PersistentOptions{{Dir: "", Port: 5432}, {Dir: t.TempDir(), Port: 0}, {Dir: t.TempDir(), Port: 70000}} {
		if _, _, err := LaunchPersistent(o); err == nil {
			t.Errorf("LaunchPersistent(%+v) should fail", o)
		}
	}
}

func TestWipePersistentRefusesAnEmptyDir(t *testing.T) {
	if err := WipePersistent(PersistentOptions{}); err == nil {
		t.Fatal("an empty Dir must never wipe the working directory")
	}
}

func execSQL(t *testing.T, dsn, q string) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("exec: %v", err)
	}
}
