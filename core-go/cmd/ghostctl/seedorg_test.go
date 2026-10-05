package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

const orgFixture = "../../../fixtures/world/org.json"

func TestSeedOrgUsageAndFileErrorsNeedNoDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var out bytes.Buffer
	if err := run([]string{"seed-org"}, &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("missing path: %v", err)
	}
	if err := run([]string{"seed-org", "a", "b"}, &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("extra args: %v", err)
	}
	err := run([]string{"seed-org", filepath.Join(t.TempDir(), "missing.json")}, &out)
	if err == nil || strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("a missing file must be reported before the database is needed, got %v", err)
	}
	bad := filepath.Join(t.TempDir(), "org.json")
	if err := os.WriteFile(bad, []byte(`{"people":[{"key":"p","kind":"employee","display_name":"X","email":"x@acme.com"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"seed-org", bad}, &out); err == nil {
		t.Error("an employee outside our domain was accepted")
	}
}

func TestSeedOrgAgainstTestDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	t.Setenv("DATABASE_URL", env.URL)
	file := abs(t, orgFixture)

	var out bytes.Buffer
	if err := run([]string{"seed-org", file}, &out); err != nil {
		t.Fatalf("first run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "seeded 3 employees: 3 created, 0 updated") {
		t.Fatalf("first run output: %s", out.String())
	}
	out.Reset()
	if err := run([]string{"seed-org", file}, &out); err != nil || !strings.Contains(out.String(), "3 employees: 0 created, 0 updated") {
		t.Fatalf("second run must change nothing: %v %s", err, out.String())
	}
	var people int
	if err := env.DB.QueryRow(`SELECT count(*) FROM people WHERE kind = 'employee'`).Scan(&people); err != nil || people != 3 {
		t.Errorf("employees = %d (%v), want 3", people, err)
	}
}
