package demorun

import (
	"errors"
	"strings"
	"testing"
)

type fakePy struct {
	exists    map[string]bool
	probeOK   map[string]bool
	created   []string
	createErr error
	look      map[string]string
}

func (f *fakePy) env(override string) PythonEnv {
	return PythonEnv{
		Override: override,
		VenvPy:   "/repo/.demo/venv/python",
		Exists:   func(p string) bool { return f.exists[p] },
		LookPath: func(n string) (string, error) {
			if p, ok := f.look[n]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Probe: func(py string) error {
			if f.probeOK[py] {
				return nil
			}
			return errors.New("import failed")
		},
		CreateVenv: func(sys string) error {
			f.created = append(f.created, sys)
			if f.createErr == nil {
				f.exists["/repo/.demo/venv/python"] = true
				f.probeOK["/repo/.demo/venv/python"] = true
			}
			return f.createErr
		},
	}
}

func TestChoosePythonPrefersAWorkingVenv(t *testing.T) {
	f := &fakePy{exists: map[string]bool{"/repo/.demo/venv/python": true}, probeOK: map[string]bool{"/repo/.demo/venv/python": true}}
	got, note, err := ChoosePython(f.env(""))
	if err != nil || got != "/repo/.demo/venv/python" || !strings.Contains(note, "venv") {
		t.Fatalf("got %q %q %v", got, note, err)
	}
}

func TestChoosePythonUsesASystemInterpreterThatAlreadyHasTheDependencies(t *testing.T) {
	f := &fakePy{exists: map[string]bool{}, probeOK: map[string]bool{"/usr/bin/python3": true}, look: map[string]string{"python": "/usr/bin/python-broken", "python3": "/usr/bin/python3"}}
	got, note, err := ChoosePython(f.env(""))
	if err != nil || got != "/usr/bin/python3" || !strings.Contains(note, "system") {
		t.Fatalf("got %q %q %v", got, note, err)
	}
	if len(f.created) != 0 {
		t.Fatal("no venv should be created when a system interpreter already works")
	}
}

func TestChoosePythonCreatesAVenvAsALastResort(t *testing.T) {
	f := &fakePy{exists: map[string]bool{}, probeOK: map[string]bool{}, look: map[string]string{"python": "/usr/bin/python"}}
	got, note, err := ChoosePython(f.env(""))
	if err != nil || got != "/repo/.demo/venv/python" || len(f.created) != 1 || f.created[0] != "/usr/bin/python" || !strings.Contains(note, "created") {
		t.Fatalf("got %q %q %v created=%v", got, note, err, f.created)
	}
}

func TestChoosePythonFailsClearlyWhenThereIsNoPython(t *testing.T) {
	f := &fakePy{exists: map[string]bool{}, probeOK: map[string]bool{}, look: map[string]string{}}
	if _, _, err := ChoosePython(f.env("")); err == nil || !strings.Contains(err.Error(), "python") {
		t.Fatalf("want an error naming python, got %v", err)
	}
	f = &fakePy{exists: map[string]bool{}, probeOK: map[string]bool{}, look: map[string]string{"python": "/usr/bin/python"}, createErr: errors.New("pip failed")}
	if _, _, err := ChoosePython(f.env("")); err == nil || !strings.Contains(err.Error(), "pip failed") {
		t.Fatalf("a venv failure must surface, got %v", err)
	}
}

func TestChoosePythonOverrideIsUsedOrRejectedNeverSilentlyReplaced(t *testing.T) {
	f := &fakePy{exists: map[string]bool{"/x/py": true}, probeOK: map[string]bool{"/x/py": true}}
	if got, _, err := ChoosePython(f.env("/x/py")); err != nil || got != "/x/py" {
		t.Fatalf("override ignored: %q %v", got, err)
	}
	f.probeOK["/x/py"] = false
	if _, _, err := ChoosePython(f.env("/x/py")); err == nil || !strings.Contains(err.Error(), "GHOST_DEMO_PYTHON") {
		t.Fatalf("a broken override must fail naming the variable, got %v", err)
	}
}

func TestNpmCommandIsResolvedThroughPathOnWindowsAndUnix(t *testing.T) {
	if got := npmNames(); len(got) == 0 || got[0] == "" {
		t.Fatal("no npm candidates")
	}
}
