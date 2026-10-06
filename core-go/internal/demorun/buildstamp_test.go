package demorun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeGit(answers map[string]string, fail string) GitRunner {
	return func(_ context.Context, _ string, args ...string) (string, error) {
		key := strings.Join(args, " ")
		if key == fail {
			return "", errors.New("git failed")
		}
		return answers[key], nil
	}
}

func TestSourceStampIsTheCoreGoTreeIdOfACleanCheckout(t *testing.T) {
	answers := map[string]string{"status --porcelain -- core-go": "", "rev-parse HEAD:core-go": "abc123"}
	if got := SourceStamp(context.Background(), fakeGit(answers, ""), "repo"); got != "abc123" {
		t.Fatalf("stamp = %q", got)
	}
}

func TestSourceStampIsEmptyWhenTheBinaryCannotBeProvenCurrent(t *testing.T) {
	answers := map[string]string{"status --porcelain -- core-go": " M core-go/cmd/core/main.go", "rev-parse HEAD:core-go": "abc123"}
	if got := SourceStamp(context.Background(), fakeGit(answers, ""), "repo"); got != "" {
		t.Fatalf("uncommitted changes must force a build, got %q", got)
	}
	clean := map[string]string{"status --porcelain -- core-go": "", "rev-parse HEAD:core-go": "abc123"}
	for _, fail := range []string{"status --porcelain -- core-go", "rev-parse HEAD:core-go"} {
		if got := SourceStamp(context.Background(), fakeGit(clean, fail), "repo"); got != "" {
			t.Fatalf("git failing on %q must force a build, got %q", fail, got)
		}
	}
}

func TestBuildCurrentNeedsTheBinaryAndAMatchingStamp(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "core.exe")
	if BuildCurrent(bin, "abc") {
		t.Fatal("no binary: not current")
	}
	if err := os.WriteFile(bin, []byte("exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if BuildCurrent(bin, "abc") {
		t.Fatal("no stamp file: not current")
	}
	if err := WriteBuildStamp(bin, "abc"); err != nil {
		t.Fatal(err)
	}
	if !BuildCurrent(bin, "abc") {
		t.Fatal("same stamp: current")
	}
	if BuildCurrent(bin, "def") {
		t.Fatal("another tree: not current")
	}
	if BuildCurrent(bin, "") {
		t.Fatal("an unknown source is never current")
	}
	if err := WriteBuildStamp(bin, ""); err != nil {
		t.Fatal(err)
	}
	if BuildCurrent(bin, "abc") {
		t.Fatal("a cleared stamp is not current")
	}
	if err := WriteBuildStamp(bin, ""); err != nil {
		t.Fatalf("clearing twice is fine: %v", err)
	}
}
