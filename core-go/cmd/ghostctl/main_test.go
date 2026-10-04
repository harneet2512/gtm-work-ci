package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		destructive bool
		want        string
		wantErr     bool
	}{
		{"up", []string{"migrate", "up"}, false, "up", false},
		{"status", []string{"migrate", "status"}, false, "status", false},
		{"down without --yes", []string{"migrate", "down"}, true, "", true},
		{"down without env switch", []string{"migrate", "down", "--yes"}, false, "", true},
		{"down fully confirmed", []string{"migrate", "down", "--yes"}, true, "down", false},
		{"unknown action", []string{"migrate", "sideways"}, false, "", true},
		{"no args", nil, false, "", true},
		{"extra args on up", []string{"migrate", "up", "now"}, false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.args, tc.destructive)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("parseArgs(%v) = %q, %v", tc.args, got, err)
			}
		})
	}
}

func TestHostOfHidesCredentials(t *testing.T) {
	if got := hostOf("postgres://user:secret@ep-x.neon.tech/ghost?sslmode=require"); got != "ep-x.neon.tech" {
		t.Fatalf("hostOf = %q", got)
	}
	if got := hostOf("::not a url"); got != "(unparseable)" {
		t.Fatalf("hostOf bad = %q", got)
	}
}

func TestRunMigrateAgainstTestDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	t.Setenv("DATABASE_URL", env.URL)

	var out bytes.Buffer
	if err := run([]string{"migrate", "status"}, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := run([]string{"migrate", "up"}, &out); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !strings.Contains(out.String(), "schema version 28") || strings.Contains(out.String(), "ghost:ghost") {
		t.Fatalf("unexpected output (or leaked credentials): %s", out.String())
	}
	t.Setenv("GHOST_ALLOW_DESTRUCTIVE", "")
	if err := run([]string{"migrate", "down", "--yes"}, &out); err == nil {
		t.Fatal("down ran without GHOST_ALLOW_DESTRUCTIVE")
	}
}
