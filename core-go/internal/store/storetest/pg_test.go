package storetest

import (
	"context"
	"testing"
)

func TestCheckSafeTestURL(t *testing.T) {
	cases := []struct {
		name, test, dev string
		ok              bool
	}{
		{"ci service db", "postgres://ghost:ghost@localhost:5432/ghost_test?sslmode=disable", "", true},
		{"neon test branch", "postgres://u:p@ep-x.neon.tech/ghost_test", "postgres://u:p@ep-x.neon.tech/ghost", true},
		{"same as dev", "postgres://u:p@h/ghost_test", "postgres://u:p@h/ghost_test", false},
		{"no test in name", "postgres://u:p@h/ghost", "", false},
		{"unparseable", "postgres://u:p@h:bad/ghost_test", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSafeTestURL(tc.test, tc.dev)
			if (err == nil) != tc.ok {
				t.Fatalf("ok=%v, err=%v", tc.ok, err)
			}
		})
	}
}

// A shared test database can hold rows an older schema rejects (here a first_party_record edge, which
// migration 0010's Down cannot represent), so resetting must not depend on Down migrations being lossless.
func TestResetClearsRowsThatOlderSchemasReject(t *testing.T) {
	ctx := context.Background()
	env, err := Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = env.Close() }()
	if _, err := env.DB.ExecContext(ctx, `INSERT INTO relationships (src_type, src_id, rel_type, dst_type, dst_id, standing, confidence, valid_from)
		VALUES ('person', gen_random_uuid(), 'works_at', 'account', gen_random_uuid(), 'first_party_record', 0.9, now())`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := reset(ctx, env.DB); err != nil {
		t.Fatalf("reset: %v", err)
	}
	var n int
	if err := env.DB.QueryRowContext(ctx, `SELECT count(*) FROM relationships`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("relationships after reset = %d, want 0", n)
	}
}
