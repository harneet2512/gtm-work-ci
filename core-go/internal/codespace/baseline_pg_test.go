package codespace

import (
	"context"
	"database/sql"
	"net"
	"path/filepath"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/harneet2512/gtm-work/core-go/internal/store/embedded"
)

// A real Postgres cluster, sealed and restored as plain files: after the live copy moved forward (Event N, new knowledge) a
// restore brings back exactly the sealed rows, with no replay, no migration and no model.
func TestBaselineRestoresARealPostgresClusterToItsSealedRows(t *testing.T) {
	if testing.Short() {
		t.Skip("starts an embedded Postgres")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	home := t.TempDir()
	opts := embedded.PersistentOptions{Dir: filepath.Join(home, "live", "pg"), Port: port, Database: "ghost_case1"}
	ctx := context.Background()
	rows := func(dsn string) (events, knowledge int) {
		t.Helper()
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM events), (SELECT count(*) FROM knowledge)`).Scan(&events, &knowledge); err != nil {
			t.Fatal(err)
		}
		return events, knowledge
	}
	exec := func(dsn, stmt string) {
		t.Helper()
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	// the one-time setup: history through N-1, then the stores stop cleanly and the baseline is sealed
	dsn, stop, err := embedded.LaunchPersistent(opts)
	if err != nil {
		t.Fatal(err)
	}
	exec(dsn, `CREATE TABLE events (n int); INSERT INTO events SELECT generate_series(1, 12);
		CREATE TABLE knowledge (id text); INSERT INTO knowledge VALUES ('k-history')`)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	b := Baseline{Home: home, Paths: []string{"live/pg/data"}}
	if _, err := b.Seal(ctx); err != nil {
		t.Fatal(err)
	}

	// the demo: Event N is processed and knowledge forms; the stores stop
	dsn, stop, err = embedded.LaunchPersistent(opts)
	if err != nil {
		t.Fatal(err)
	}
	exec(dsn, `INSERT INTO events VALUES (13); INSERT INTO knowledge VALUES ('k-new')`)
	if e, k := rows(dsn); e != 13 || k != 2 {
		t.Fatalf("after the play rows = %d/%d", e, k)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	// Reset: the stores are down, the baseline replaces the cluster, the stores restart
	stats, err := b.Restore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("restored %d files, %d MB, in %s", stats.Files, stats.Bytes>>20, stats.Duration)
	dsn, stop, err = embedded.LaunchPersistent(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stop() })
	if e, k := rows(dsn); e != 12 || k != 1 {
		t.Fatalf("after the restore rows = %d events / %d knowledge, want the sealed 12 / 1", e, k)
	}
}
