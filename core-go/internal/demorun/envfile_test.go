package demorun

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secretValue = "sk-or-v1-THIS-MUST-NEVER-BE-PRINTED"

const utf8BOM = "\xef\xbb\xbf"

func TestParseEnvReadsKeysAndSkipsNoise(t *testing.T) {
	src := "# comment\n\nA=1\nexport B = two words \nC=\"quoted value\"\nD='single'\nE=a=b=c\nnot a pair\n  # indented comment\nF=\n"
	got, err := ParseEnv(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseEnv: %v", err)
	}
	want := map[string]string{"A": "1", "B": "two words", "C": "quoted value", "D": "single", "E": "a=b=c", "F": ""}
	if len(got) != len(want) {
		t.Fatalf("got %d keys (%v), want %d", len(got), got.Names(), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestParseEnvToleratesWindowsLineEndingsAndBOM(t *testing.T) {
	got, err := ParseEnv(strings.NewReader(utf8BOM + "A=1\r\nB=2\r\n"))
	if err != nil {
		t.Fatalf("ParseEnv: %v", err)
	}
	if got["A"] != "1" || got["B"] != "2" {
		t.Fatalf("got %v", got.Names())
	}
}

func TestEnvNeverFormatsValues(t *testing.T) {
	env := Env{"OPENROUTER_API_KEY": secretValue, "GHOST_MODEL": "m"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		out := fmt.Sprintf(verb, env)
		if strings.Contains(out, secretValue) {
			t.Errorf("%s leaked a value: %s", verb, out)
		}
		if !strings.Contains(out, "OPENROUTER_API_KEY") {
			t.Errorf("%s dropped the variable name: %s", verb, out)
		}
	}
	// A pointer and a slice of Env values must be safe too.
	if out := fmt.Sprint(&env, []Env{env}); strings.Contains(out, secretValue) {
		t.Errorf("composite formatting leaked: %s", out)
	}
}

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	if _, found, err := LoadEnvFile(filepath.Join(dir, "missing.env")); err != nil || found {
		t.Fatalf("a missing file is not an error: found=%v err=%v", found, err)
	}
	if _, _, err := LoadEnvFile(dir); err == nil {
		t.Fatal("reading a directory must fail")
	}
	p := filepath.Join(dir, ".env")
	if werr := os.WriteFile(p, []byte("K="+secretValue+"\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	env, found, err := LoadEnvFile(p)
	if err != nil || !found || env["K"] != secretValue {
		t.Fatalf("LoadEnvFile: found=%v err=%v", found, err)
	}
}

func TestEnvironIsSortedAndSkipsEmptyNames(t *testing.T) {
	got := Env{"B": "2", "A": "1", "": "x"}.Environ()
	if len(got) != 2 || got[0] != "A=1" || got[1] != "B=2" {
		t.Fatalf("Environ = %v", got)
	}
}

func TestMergeLaterWinsAndDoesNotMutate(t *testing.T) {
	base := Env{"A": "1", "B": "2"}
	over := Env{"B": "3", "C": "4"}
	m := Merge(base, over)
	if m["A"] != "1" || m["B"] != "3" || m["C"] != "4" {
		t.Fatalf("Merge wrong: %v", m.Names())
	}
	if base["B"] != "2" || len(base) != 2 {
		t.Fatal("Merge mutated its input")
	}
}

func TestProcessEnvOverlaysAndDropsEmptyValues(t *testing.T) {
	t.Setenv("GHOST_DEMO_TEST_A", "from-process")
	t.Setenv("GHOST_DEMO_TEST_EMPTY", "")
	e := ProcessEnv()
	if e["GHOST_DEMO_TEST_A"] != "from-process" {
		t.Fatal("process variable missing")
	}
	if _, ok := e["GHOST_DEMO_TEST_EMPTY"]; ok {
		t.Fatal("empty process variables must not shadow .env values")
	}
}
