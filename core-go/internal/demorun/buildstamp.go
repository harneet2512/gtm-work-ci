package demorun

import (
	"context"
	"fmt"
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

// EnvSkipBuild, when 1 or true, makes `up` never compile anything: a detached launch has no console, and the Go toolchain
// dies there with 0xc000013a (a console control event). The binaries must have been prebuilt from a normal shell.
const EnvSkipBuild = "GHOST_DEMO_SKIP_BUILD"

// SkipBuildEnabled reads EnvSkipBuild through get (os.Getenv in production).
func SkipBuildEnabled(get func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(get(EnvSkipBuild))) {
	case "1", "true":
		return true
	}
	return false
}

// CheckPrebuilt is the skip-build gate: binary must exist and carry a stamp equal to the tree's (HEAD:core-go). It never
// builds and never lets stale code run; a tree with uncommitted core-go changes has no stamp, so it is refused too.
func CheckPrebuilt(svc, binary, stamp string) error {
	if stamp == "" {
		return fmt.Errorf("demorun: %s=1 but core-go has uncommitted changes (or git does not answer), so no binary can be proven current; commit, then prebuild interactively", EnvSkipBuild)
	}
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("demorun: %s=1 but %s is missing at %s; prebuild interactively with `ghostctl codespace up`", EnvSkipBuild, svc, binary)
	}
	b, _ := os.ReadFile(stampFile(binary))
	if have := strings.TrimSpace(string(b)); have != stamp {
		return fmt.Errorf("demorun: %s is stale: binary stamp %q, tree %q; prebuild interactively (a detached launch must not compile)", svc, have, stamp)
	}
	return nil
}
