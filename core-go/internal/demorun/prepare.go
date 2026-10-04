package demorun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// PythonEnv abstracts what ChoosePython needs from the machine, so the choice is testable.
type PythonEnv struct {
	Override   string // GHOST_DEMO_PYTHON
	VenvPy     string // interpreter inside .demo/venv
	Exists     func(path string) bool
	LookPath   func(name string) (string, error)
	Probe      func(python string) error // imports the worker's dependencies
	CreateVenv func(systemPython string) error
}

// ChoosePython picks the interpreter that runs the worker: an explicit override (used or rejected, never replaced),
// else a working .demo/venv, else a system interpreter that already has the dependencies, else a new venv with
// `pip install -e worker-py`. note says which and why.
func ChoosePython(e PythonEnv) (python, note string, err error) {
	if e.Override != "" {
		if err := e.Probe(e.Override); err != nil {
			return "", "", fmt.Errorf("demorun: GHOST_DEMO_PYTHON (%s) cannot import the worker's dependencies: %w", e.Override, err)
		}
		return e.Override, "using GHOST_DEMO_PYTHON", nil
	}
	if e.Exists(e.VenvPy) && e.Probe(e.VenvPy) == nil {
		return e.VenvPy, "using the demo venv", nil
	}
	var system string
	for _, name := range []string{"python", "python3"} {
		p, lerr := e.LookPath(name)
		if lerr != nil {
			continue
		}
		if system == "" {
			system = p
		}
		if e.Probe(p) == nil {
			return p, "using system python (" + p + ") which already has the worker's dependencies", nil
		}
	}
	if system == "" {
		return "", "", errors.New("demorun: no python found on PATH (need Python 3.12+ for the worker); install it or set GHOST_DEMO_PYTHON")
	}
	if err := e.CreateVenv(system); err != nil {
		return "", "", fmt.Errorf("demorun: create the worker venv with %s: %w", system, err)
	}
	return e.VenvPy, "created the demo venv and installed worker-py", nil
}

// VenvPython is the interpreter path inside a venv.
func VenvPython(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts", "python.exe")
	}
	return filepath.Join(venvDir, "bin", "python")
}

func npmNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"npm.cmd", "npm"}
	}
	return []string{"npm"}
}

// PrepareOptions tune Prepare.
type PrepareOptions struct {
	NoSlack, NoWeb bool
	Out            io.Writer
}

// Prepare builds what `demo up` runs: the ghostctl host copy, core and slackbot binaries, the worker's Python
// interpreter and the web dependencies. A binary whose service is running is not rebuilt (Windows locks it).
func Prepare(ctx context.Context, l Layout, sup Supervisor, o PrepareOptions) (Tooling, error) {
	say := func(format string, args ...any) {
		if o.Out != nil {
			fmt.Fprintf(o.Out, format+"\n", args...)
		}
	}
	if err := os.MkdirAll(l.BinDir(), 0o755); err != nil {
		return Tooling{}, err
	}
	var t Tooling
	var err error
	if t.Host, err = installHost(l, sup); err != nil {
		return Tooling{}, err
	}
	build := func(svc, pkg string) (string, error) {
		out := filepath.Join(l.BinDir(), svc+exeSuffix())
		if st, _ := sup.PIDs.State(svc); st == StateRunning {
			if _, err := os.Stat(out); err == nil {
				return out, nil
			}
		}
		say("building %s ...", svc)
		cmd := exec.CommandContext(ctx, "go", "build", "-o", out, pkg)
		cmd.Dir = filepath.Join(l.Root, "core-go")
		if b, err := cmd.CombinedOutput(); err != nil {
			return "", &ServiceError{Service: svc, Phase: "build", Err: fmt.Errorf("go build %s: %v\n%s", pkg, err, b)}
		}
		return out, nil
	}
	if t.Core, err = build(SvcCore, "./cmd/core"); err != nil {
		return Tooling{}, err
	}
	if !o.NoSlack {
		if t.Slackbot, err = build(SvcSlackbot, "./cmd/slackbot"); err != nil {
			return Tooling{}, err
		}
	}
	py, note, err := ChoosePython(realPythonEnv(ctx, l, say))
	if err != nil {
		return Tooling{}, &ServiceError{Service: SvcWorker, Phase: "build", Err: err}
	}
	say("worker python: %s", note)
	t.Python = py
	if !o.NoWeb {
		if t.Npm, err = prepareWeb(ctx, l, say); err != nil {
			return Tooling{}, err
		}
	}
	return t, nil
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// installHost copies the running ghostctl next to the other binaries, so the long-lived store hosts do not run
// from a `go run` temp file that is deleted when the command exits. A copy that is locked by a running host is
// left alone.
func installHost(l Layout, sup Supervisor) (string, error) {
	dst := filepath.Join(l.BinDir(), "ghostctl-host"+exeSuffix())
	for _, svc := range []string{SvcNeo4j, SvcPostgres} {
		if st, _ := sup.PIDs.State(svc); st == StateRunning {
			if _, err := os.Stat(dst); err == nil {
				return dst, nil
			}
		}
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("demorun: locate ghostctl: %w", err)
	}
	if err := copyFile(self, dst); err != nil {
		return "", fmt.Errorf("demorun: install the host binary: %w", err)
	}
	return dst, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	_ = os.Remove(dst)
	return os.Rename(tmp, dst)
}

func realPythonEnv(ctx context.Context, l Layout, say func(string, ...any)) PythonEnv {
	probeCode := "import ghost_worker, fastapi, uvicorn, litellm"
	return PythonEnv{
		Override: os.Getenv("GHOST_DEMO_PYTHON"),
		VenvPy:   VenvPython(l.VenvDir()),
		Exists:   func(p string) bool { _, err := os.Stat(p); return err == nil },
		LookPath: exec.LookPath,
		Probe: func(py string) error {
			c, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(c, py, "-c", probeCode)
			cmd.Dir = filepath.Join(l.Root, "worker-py")
			if b, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("%v: %s", err, lastLine(string(b)))
			}
			return nil
		},
		CreateVenv: func(system string) error {
			say("creating the worker venv (first run only; installs litellm, this takes a few minutes) ...")
			if b, err := exec.CommandContext(ctx, system, "-m", "venv", l.VenvDir()).CombinedOutput(); err != nil {
				return fmt.Errorf("python -m venv: %v: %s", err, lastLine(string(b)))
			}
			cmd := exec.CommandContext(ctx, VenvPython(l.VenvDir()), "-m", "pip", "install", "-e", filepath.Join(l.Root, "worker-py"))
			if b, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("pip install -e worker-py: %v: %s", err, lastLine(string(b)))
			}
			return nil
		},
	}
}

func prepareWeb(ctx context.Context, l Layout, say func(string, ...any)) (string, error) {
	var npm string
	for _, n := range npmNames() {
		if p, err := exec.LookPath(n); err == nil {
			npm = p
			break
		}
	}
	if npm == "" {
		return "", &ServiceError{Service: SvcWeb, Phase: "build", Err: errors.New("npm not found on PATH (Node 22.12+ is required); install Node or run `demo up --no-web`")}
	}
	web := filepath.Join(l.Root, "web")
	if _, err := os.Stat(filepath.Join(web, "node_modules")); err != nil {
		say("installing web dependencies (first run only) ...")
		cmd := exec.CommandContext(ctx, npm, "ci")
		cmd.Dir = web
		if b, err := cmd.CombinedOutput(); err != nil {
			return "", &ServiceError{Service: SvcWeb, Phase: "build", Err: fmt.Errorf("npm ci: %v: %s", err, lastLine(string(b)))}
		}
	}
	return npm, nil
}

func lastLine(s string) string {
	lines := splitLines(s)
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		if r != '\r' {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
