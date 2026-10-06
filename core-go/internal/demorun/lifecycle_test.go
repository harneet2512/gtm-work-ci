package demorun

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpStopsAtTheFirstFailingServiceAndNamesIt(t *testing.T) {
	sup, out := newSupervisor(t)
	good, _ := helperSpec(t, "neo4j", "serve", nil)
	bad, _ := helperSpec(t, "postgres", "die", nil)
	never, _ := helperSpec(t, "core", "serve", nil)
	err := Up(context.Background(), sup, []Spec{good, bad, never})
	if err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("Up must fail naming postgres, got %v", err)
	}
	defer sup.Stop(context.Background(), good)
	if st, _ := sup.PIDs.State("neo4j"); st != StateRunning {
		t.Fatal("services that came up before the failure stay up so a retry resumes quickly")
	}
	if st, _ := sup.PIDs.State("core"); st != StateStopped {
		t.Fatal("services after the failure must never be started")
	}
	if !strings.Contains(out.String(), "FAILED") {
		t.Fatalf("output should say which service failed: %q", out.String())
	}
}

func TestUpRefusesAServiceWhosePortIsBusy(t *testing.T) {
	sup, _ := newSupervisor(t)
	spec, port := helperSpec(t, "core", "serve", nil)
	spec.Port = port
	if err := sup.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	defer sup.Stop(context.Background(), spec)
	// Same port, but under another name that is not running: the occupant is not ours.
	other := spec
	other.Name = "web"
	if err := Up(context.Background(), sup, []Spec{other}); err == nil || !strings.Contains(err.Error(), "web") || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("a busy port must stop Up with the service named, got %v", err)
	}
}

func TestDownStopsInReverseOrderAndToleratesStoppedServices(t *testing.T) {
	sup, out := newSupervisor(t)
	a, _ := helperSpec(t, "neo4j", "serve", nil)
	b, _ := helperSpec(t, "core", "serve", nil)
	if err := Up(context.Background(), sup, []Spec{a, b}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Down(context.Background(), sup, []Spec{a, b, {Name: "web"}}); err != nil {
		t.Fatalf("Down: %v", err)
	}
	s := out.String()
	if strings.Index(s, "core: stopped") < 0 || strings.Index(s, "neo4j: stopped") < 0 || strings.Index(s, "core: stopped") > strings.Index(s, "neo4j: stopped") {
		t.Fatalf("core must stop before neo4j (reverse start order): %q", s)
	}
}

func TestResetRequiresConfirmationAndWipesOnlyDemoData(t *testing.T) {
	root := t.TempDir()
	l := NewLayout(root)
	keep := []string{l.SecretsFile(), filepath.Join(l.BinDir(), "core.exe"), filepath.Join(l.VenvDir(), "pyvenv.cfg"), filepath.Join(root, "data", "crmarena_b2b", "x.json"), filepath.Join(root, ".env")}
	wipe := []string{filepath.Join(l.PGDir(), "data", "PG_VERSION"), filepath.Join(l.Neo4jDir(), "data", "x"), filepath.Join(l.ReplayEventsDir(), "e.json"),
		l.ManifestFile(), l.StateFile(), l.LogFile("core")}
	for _, p := range append(append([]string{}, keep...), wipe...) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := Reset(context.Background(), Supervisor{Layout: l, PIDs: l.PIDs(), Out: &out}, nil, false); err == nil {
		t.Fatal("Reset without confirmation must refuse")
	}
	for _, p := range wipe {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("refused Reset must not delete %s", p)
		}
	}
	if err := Reset(context.Background(), Supervisor{Layout: l, PIDs: l.PIDs(), Out: &out}, nil, true); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	for _, p := range wipe {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("Reset should have removed %s", p)
		}
	}
	for _, p := range keep {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("Reset must keep %s: %v", p, err)
		}
	}
}
