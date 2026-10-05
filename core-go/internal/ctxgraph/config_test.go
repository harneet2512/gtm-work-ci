package ctxgraph

import "testing"

func TestConfigFromEnv(t *testing.T) {
	cases := []struct {
		name, uri, user, pass string
		wantOK, wantErr       bool
		wantUser              string
	}{
		{"not configured", "", "", "", false, false, ""},
		{"credentials", "bolt://x:7687", "", "pw", true, false, "neo4j"},
		{"explicit user", "bolt://x:7687", "alice", "pw", true, false, "alice"},
		{"auth disabled", "bolt://x:7687", "", "", true, false, ""},
		{"user without password", "bolt://x:7687", "alice", "", false, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("NEO4J_URI", c.uri)
			t.Setenv("NEO4J_USER", c.user)
			t.Setenv("NEO4J_PASSWORD", c.pass)
			t.Setenv("NEO4J_DATABASE", "")
			cfg, ok, err := ConfigFromEnv()
			if ok != c.wantOK || (err != nil) != c.wantErr || (ok && cfg.User != c.wantUser) {
				t.Fatalf("ConfigFromEnv = %+v %v %v", cfg, ok, err)
			}
			if ok && cfg.Database != "neo4j" {
				t.Errorf("database = %q", cfg.Database)
			}
		})
	}
}
