package embedded

import (
	"context"
	"testing"
)

func TestStartGivesAMigratedPrivateDatabaseThatClosesCleanly(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Postgres")
	}
	t.Setenv("TEST_DATABASE_URL", "postgres://nobody:nothing@127.0.0.1:1/never_test?sslmode=disable")
	t.Setenv("DATABASE_URL", "postgres://nobody:nothing@127.0.0.1:1/never?sslmode=disable")
	env, err := Start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var n int
	if err := env.DB.QueryRow(`SELECT count(*) FROM demo_manifests`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the latest migration is not applied or the database is not empty: %d, %v", n, err)
	}
	if err := env.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
