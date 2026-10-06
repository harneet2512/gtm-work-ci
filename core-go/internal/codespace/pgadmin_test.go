package codespace

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/embedded"
)

func TestAdminDSNAndDSNForSwapOnlyTheDatabase(t *testing.T) {
	const dsn = "postgres://ghost:ghost@127.0.0.1:15432/ghost_demo?sslmode=disable"
	got, err := AdminDSN(dsn)
	if err != nil || got != "postgres://ghost:ghost@127.0.0.1:15432/postgres?sslmode=disable" {
		t.Fatalf("AdminDSN = %q, %v", got, err)
	}
	got, err = DSNFor(dsn, "ghost_case2")
	if err != nil || got != "postgres://ghost:ghost@127.0.0.1:15432/ghost_case2?sslmode=disable" {
		t.Fatalf("DSNFor = %q, %v", got, err)
	}
	for _, bad := range []string{"", "not a url", "/just/a/path"} {
		if _, err := AdminDSN(bad); err == nil {
			t.Errorf("AdminDSN(%q) accepted", bad)
		}
		if _, err := DSNFor(bad, "ghost_case2"); err == nil {
			t.Errorf("DSNFor(%q) accepted", bad)
		}
	}
	if _, err := DSNFor(dsn, `x"; DROP`); err == nil {
		t.Error("DSNFor accepted a name that is not an identifier")
	}
}

func TestQuoteIdentRefusesAnythingButAPlainName(t *testing.T) {
	if q, err := quoteIdent("ghost_case1"); err != nil || q != `"ghost_case1"` {
		t.Fatalf("quoteIdent = %q, %v", q, err)
	}
	for _, bad := range []string{"", "Ghost", "1abc", `a"b`, "a b", "a;b", strings.Repeat("a", 64)} {
		if _, err := quoteIdent(bad); err == nil {
			t.Errorf("quoteIdent(%q) accepted", bad)
		}
	}
}

func TestPGAdminRefusesBadNamesBeforeConnecting(t *testing.T) {
	a := PGAdmin{DSN: "postgres://ghost:ghost@127.0.0.1:1/ghost_demo?sslmode=disable"} // nothing listens: reaching the server would fail differently
	ctx := context.Background()
	for name, err := range map[string]error{
		"exists": func() error { _, e := a.Exists(ctx, "Bad;"); return e }(),
		"create": a.Create(ctx, "Bad;"),
		"drop":   a.Drop(ctx, "Bad;"),
		"clone":  a.Clone(ctx, "ok_name", "Bad;"),
	} {
		if err == nil || !strings.Contains(err.Error(), "not a valid database name") {
			t.Errorf("%s: err = %v, want a name refusal before any connection", name, err)
		}
	}
	if err := a.Create(ctx, "ok_name"); err == nil || !strings.Contains(err.Error(), "not answering") {
		t.Errorf("an unreachable server must be reported as such: %v", err)
	}
}

// TestPGAdminOnARealPostgres is what makes Reset trustworthy: a copy made with CREATE DATABASE ... TEMPLATE holds the
// rows of the source at the time of the copy, restoring over a database in use drops its sessions, and none of it needs
// the history to be replayed.
func TestPGAdminOnARealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Postgres")
	}
	ctx := context.Background()
	dsn, stop, err := embedded.Launch()
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = stop() }) // also runs after t.Fatal and a failed subtest
	admin := PGAdmin{DSN: dsn}

	exists := func(name string) bool {
		t.Helper()
		ok, err := admin.Exists(ctx, name)
		if err != nil {
			t.Fatalf("Exists(%s): %v", name, err)
		}
		return ok
	}
	open := func(name string) *sql.DB {
		t.Helper()
		d, err := DSNFor(dsn, name)
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("pgx", d)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	count := func(db *sql.DB) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM events`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	if exists("demo_a") {
		t.Fatal("demo_a must not exist yet")
	}
	if err := admin.Create(ctx, "demo_a"); err != nil || !exists("demo_a") {
		t.Fatalf("Create: %v exists=%v", err, exists("demo_a"))
	}
	live := open("demo_a")
	if _, err := live.ExecContext(ctx, `CREATE TABLE events (n int); INSERT INTO events VALUES (1), (2)`); err != nil {
		t.Fatal(err)
	}

	// Snapshot while a session is still open on the source: Clone must end it, not fail.
	if err := admin.Clone(ctx, "demo_a_frozen", "demo_a"); err != nil {
		t.Fatalf("Clone with an open session: %v", err)
	}
	// The source keeps working after its sessions were ended (a new pool connection is made).
	live = open("demo_a")
	if _, err := live.ExecContext(ctx, `INSERT INTO events VALUES (3)`); err != nil { // "Event N" played
		t.Fatal(err)
	}
	if got := count(live); got != 3 {
		t.Fatalf("live rows = %d, want 3", got)
	}

	// Reset: drop the played database (a session is open on it) and restore from the template.
	if err := admin.Drop(ctx, "demo_a"); err != nil || exists("demo_a") {
		t.Fatalf("Drop: %v exists=%v", err, exists("demo_a"))
	}
	if err := admin.Clone(ctx, "demo_a", "demo_a_frozen"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := count(open("demo_a")); got != 2 {
		t.Fatalf("restored rows = %d, want the 2 that were frozen", got)
	}
	if got := count(open("demo_a_frozen")); got != 2 {
		t.Fatalf("the template changed: %d rows", got)
	}
	if err := admin.Drop(ctx, "never_existed"); err != nil {
		t.Fatalf("dropping a missing database must be a no-op: %v", err)
	}
	if err := admin.Create(ctx, "demo_a"); err == nil {
		t.Fatal("creating an existing database must fail, not silently succeed")
	}
}
