package demorun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkipBuildEnabledReadsTheEnvironmentFlag(t *testing.T) {
	for val, want := range map[string]bool{"1": true, "true": true, "TRUE": true, "": false, "0": false, "no": false} {
		got := SkipBuildEnabled(func(string) string { return val })
		if got != want {
			t.Errorf("%s=%q: got %v, want %v", EnvSkipBuild, val, got, want)
		}
	}
}

func prebuilt(t *testing.T, stamp string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "core.exe")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteBuildStamp(bin, stamp); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestCheckPrebuiltAcceptsABinaryWhoseStampIsTheTree(t *testing.T) {
	if err := CheckPrebuilt("core", prebuilt(t, "tree1"), "tree1"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPrebuiltRefusesAStaleStampNamingBothAndNeverBuilds(t *testing.T) {
	err := CheckPrebuilt("core", prebuilt(t, "old"), "tree1")
	if err == nil || !strings.Contains(err.Error(), "old") || !strings.Contains(err.Error(), "tree1") || !strings.Contains(err.Error(), "prebuild") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckPrebuiltRefusesAMissingBinaryAndADirtyTree(t *testing.T) {
	if err := CheckPrebuilt("core", filepath.Join(t.TempDir(), "none.exe"), "tree1"); err == nil {
		t.Fatal("a missing binary must be refused")
	}
	err := CheckPrebuilt("core", prebuilt(t, "tree1"), "")
	if err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("a dirty tree has no stamp and must be refused: %v", err)
	}
}
