package codespace

import (
	"path/filepath"
	"testing"
)

func TestDemoHomePrefersGhostDemoHomeThenTheRunnersOverrideThenDDrive(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		goos string
		want string
	}{
		{"explicit home", map[string]string{"GHOST_DEMO_HOME": `E:\demo`, "GHOST_DEMO_DIR": "x"}, "windows", `E:\demo`},
		{"runner override", map[string]string{"GHOST_DEMO_DIR": "/srv/demo"}, "linux", "/srv/demo"},
		{"windows default", map[string]string{}, "windows", `D:\ghost-demo`},
		{"blank values are unset", map[string]string{"GHOST_DEMO_HOME": "  "}, "windows", `D:\ghost-demo`},
		{"other systems keep the runner default", map[string]string{}, "linux", ""},
	} {
		if got := DemoHome(tc.env, tc.goos); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNeoCacheLivesUnderTheHome(t *testing.T) {
	if got := NeoCacheDir(`D:\ghost-demo`); got != filepath.Join(`D:\ghost-demo`, "cache", "neo4j") {
		t.Errorf("cache = %s", got)
	}
	if NeoCacheDir("") != "" {
		t.Error("no home, no cache override")
	}
}
