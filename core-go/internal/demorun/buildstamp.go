package demorun

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// GitRunner runs git in dir and returns its trimmed output; tests replace it.
type GitRunner func(ctx context.Context, dir string, args ...string) (string, error)

// RunGit is the real GitRunner.
func RunGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// SourceStamp identifies the Go sources the binaries are built from: the git tree id of core-go. It is "" whenever the
// binaries cannot be proven current, that is when git does not answer or core-go has uncommitted changes, and a build
// is then always made. A binary built from a tree with the same id is the same program, whichever checkout made it.
func SourceStamp(ctx context.Context, git GitRunner, root string) string {
	dirty, err := git(ctx, root, "status", "--porcelain", "--", "core-go")
	if err != nil || dirty != "" {
		return ""
	}
	id, err := git(ctx, root, "rev-parse", "HEAD:core-go")
	if err != nil {
		return ""
	}
	return id
}

func stampFile(binary string) string { return binary + ".stamp" }

// BuildCurrent reports whether binary exists and was built from the sources the stamp names, so that a Start does not run
// `go build` (about 20 seconds of linking) for a program that has not changed.
func BuildCurrent(binary, stamp string) bool {
	if stamp == "" {
		return false
	}
	if _, err := os.Stat(binary); err != nil {
		return false
	}
	b, err := os.ReadFile(stampFile(binary))
	return err == nil && strings.TrimSpace(string(b)) == stamp
}

// WriteBuildStamp records what binary was built from; with no stamp it removes any earlier one.
func WriteBuildStamp(binary, stamp string) error {
	if stamp == "" {
		err := os.Remove(stampFile(binary))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(stampFile(binary), []byte(stamp+"\n"), 0o644)
}
