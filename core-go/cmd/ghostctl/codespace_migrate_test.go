package main

import (
	"context"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store"
	"github.com/harneet2512/gtm-work/core-go/internal/store/embedded"
)

func TestMigrateDatabaseAppliesTheSchemaToAFreshCaseDatabaseAndRefusesAnUnreachableOne(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Postgres")
	}
	ctx := context.Background()
	if err := migrateDatabase(ctx, "postgres://ghost:ghost@127.0.0.1:1/none?sslmode=disable"); err == nil {
		t.Fatal("an unreachable database must be an error")
	}
	dsn, stop, err := embedded.Launch()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stop() }()
	if err := migrateDatabase(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := migrateDatabase(ctx, dsn); err != nil {
		t.Fatalf("migrating twice is a no-op: %v", err)
	}
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM knowledge`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the schema must exist and be empty: %d %v", n, err)
	}
}
