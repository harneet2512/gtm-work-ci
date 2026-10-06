package neo4jtest

import (
	"context"
	"strings"
	"testing"
)

func TestPersistentNeo4jOptionsAreValidatedBeforeAnythingStarts(t *testing.T) {
	for name, tc := range map[string]struct {
		o    PersistentOptions
		want string
	}{
		"no directory": {PersistentOptions{BoltPort: 17687, Password: "longenough"}, "needs a directory"},
		"port zero":    {PersistentOptions{Dir: t.TempDir(), Password: "longenough"}, "not a TCP port"},
		"port too big": {PersistentOptions{Dir: t.TempDir(), BoltPort: 70000, Password: "longenough"}, "not a TCP port"},
		"short secret": {PersistentOptions{Dir: t.TempDir(), BoltPort: 17687, Password: "short"}, "at least 8 characters"},
	} {
		env, err := StartPersistent(context.Background(), tc.o)
		if err == nil || env != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestWipePersistentRefusesAnEmptyDirectory(t *testing.T) {
	if err := WipePersistent(""); err == nil {
		t.Fatal("an empty directory must never be wiped")
	}
	if err := WipePersistent(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestJavaExeNamesTheLauncherOfThisPlatform(t *testing.T) {
	if got := javaExe(); got != "java" && got != "java.exe" {
		t.Fatalf("javaExe = %s", got)
	}
}
