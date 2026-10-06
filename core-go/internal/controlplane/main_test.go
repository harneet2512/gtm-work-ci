package controlplane_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/controlplane"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

const missing = "99999999-9999-4999-8999-999999999999"

func reader(t *testing.T) *controlplane.Reader {
	t.Helper()
	r, err := controlplane.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func scalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s *string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if s == nil {
		return "<null>"
	}
	return *s
}

func exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := env.DB.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// seedAccount makes a fresh account with a state and one activity, the minimum strategytest.Seed needs, so every test
// owns its account and its run history.
func seedAccount(t *testing.T, name string) string {
	t.Helper()
	accountID := scalar(t, `INSERT INTO accounts (name, domain) VALUES ($1, $2) RETURNING id::text`, name,
		strings.ToLower(strings.ReplaceAll(name, " ", ""))+".example.test")
	// The send-time re-evaluation reads account_id out of the state document and refuses a state of another account.
	state := `{"account_id":"` + accountID + `","version":1,"headline":{"stage":{"value":"negotiation"}}}`
	exec(t, `INSERT INTO state_history (account_id, version, as_of, state) VALUES ($1::uuid, 1, '2025-12-31T00:00:00Z', $2::jsonb)`, accountID, state)
	exec(t, `INSERT INTO account_state (account_id, version, as_of, state) VALUES ($1::uuid, 1, '2025-12-31T00:00:00Z', $2::jsonb)`, accountID, state)
	exec(t, `WITH se AS (
  INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload, occurred_at)
  VALUES ('email', $2, 'k', encode(sha256(convert_to($2::text, 'UTF8')), 'hex'), '{}'::jsonb, '2026-01-02T03:04:05Z') RETURNING id)
INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, summary, provenance)
SELECT id, 'EmailReceived', 'email', $2, '2026-01-02T03:04:05Z', $1::uuid, 'Marco asks for the security documents',
       ('{"source_system":"email","source_object_id":"' || $2 || '"}')::jsonb FROM se`, accountID, "obj-"+name)
	return accountID
}

// seedEpisode seeds one awaiting-choice episode (run, strategy set, three candidates, bundles) on the account.
func seedEpisode(t *testing.T, accountID string) strategytest.Seeded {
	t.Helper()
	// Runs created in one transaction-less burst can share a microsecond; separate them so "previous run" is well defined.
	time.Sleep(5 * time.Millisecond)
	return strategytest.Seed(t, env.DB, accountID)
}

// repoFile resolves a repo-relative path by walking up from the working directory.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, rel)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("%s not found above the working directory", rel)
		}
		dir = parent
	}
}
