package abcrun

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"sync"
)

// A recorded model call is keyed by its whole prompt, and the prompt carries ids the database mints (run, account,
// person, activity, claim, context-access ids). To replay a recording the world must therefore mint the same ids on
// every build. InstallDeterministicIDs rewrites every column default gen_random_uuid() of the public schema to a
// function that draws from a sequence, and SeedIDs repositions that sequence from a name, so each situation and arm
// mints the same ids whatever ran before it. It touches only the private database an experiment owns.
const installIDs = `
CREATE SEQUENCE IF NOT EXISTS abc_uuid_seq;
CREATE OR REPLACE FUNCTION abc_uuid() RETURNS uuid LANGUAGE sql VOLATILE AS $f$
  SELECT (substr(h, 1, 12) || '4' || substr(h, 14, 3) || '8' || substr(h, 18, 3) || substr(h, 21, 12))::uuid
    FROM (SELECT md5('ghost-abc-uuid-' || nextval('abc_uuid_seq')::text) AS h) t
$f$;
DO $do$
DECLARE r record;
BEGIN
  FOR r IN SELECT c.relname AS tbl, a.attname AS col
             FROM pg_attrdef d
             JOIN pg_class c ON c.oid = d.adrelid
             JOIN pg_namespace n ON n.oid = c.relnamespace
             JOIN pg_attribute a ON a.attrelid = d.adrelid AND a.attnum = d.adnum
            WHERE n.nspname = 'public' AND pg_get_expr(d.adbin, d.adrelid) = 'gen_random_uuid()'
  LOOP
    EXECUTE format('ALTER TABLE %I ALTER COLUMN %I SET DEFAULT abc_uuid()', r.tbl, r.col);
  END LOOP;
END
$do$;`

// InstallDeterministicIDs makes every id the database mints a function of a sequence position.
func InstallDeterministicIDs(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, installIDs); err != nil {
		return fmt.Errorf("abcrun: install deterministic ids: %w", err)
	}
	return nil
}

// SeedIDs positions the id sequence from a name: the same name always starts the same ids.
func SeedIDs(ctx context.Context, db *sql.DB, name string) error {
	sum := sha256.Sum256([]byte(name))
	pos := int64(sum[0])<<40 | int64(sum[1])<<32 | int64(sum[2])<<24 | int64(sum[3])<<16 | int64(sum[4])<<8 | int64(sum[5])
	if _, err := db.ExecContext(ctx, `SELECT setval('abc_uuid_seq', $1::bigint, false)`, pos); err != nil {
		return fmt.Errorf("abcrun: seed ids from %q: %w", name, err)
	}
	// context_access_log.id (a bigserial) is the access id the worker sees in every context packet, so it is part of the
	// prompt too: position it from the name as well (a large base keeps the runs of one database apart).
	if _, err := db.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('context_access_log', 'id'), $1::bigint, false)`, (pos%1_000_000_000)*1000+1); err != nil {
		return fmt.Errorf("abcrun: seed access ids from %q: %w", name, err)
	}
	return nil
}

// Sequence is the deterministic id generator the orchestrator's Config.NewID uses: the n-th id of a named run.
type Sequence struct {
	mu   sync.Mutex
	name string
	n    int
}

// Reset starts a new named run's ids from the beginning.
func (s *Sequence) Reset(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.name, s.n = name, 0
}

// Next returns the next id: an RFC 4122 version-5-shaped uuid of the name and position.
func (s *Sequence) Next() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	sum := sha256.Sum256([]byte(fmt.Sprintf("ghost-abc-id|%s|%d", s.name, s.n)))
	h := fmt.Sprintf("%x", sum[:16])
	return strings.Join([]string{h[0:8], h[8:12], "4" + h[13:16], "8" + h[17:20], h[20:32]}, "-")
}
