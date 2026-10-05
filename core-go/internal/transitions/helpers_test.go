package transitions

import (
	"os"
	"path/filepath"
	"testing"
)

// repoFile finds a repository file by walking up from the package directory.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("%s not found", rel)
		}
		dir = parent
	}
}

func loadRules(t *testing.T) RuleSet {
	t.Helper()
	rs, err := LoadRules(repoFile(t, "contracts/transitions/rules.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}
