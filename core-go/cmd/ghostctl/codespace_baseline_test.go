package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/codespace"
)

func TestSetupSealsTheBaselineOnlyAfterEverythingWasBuiltAndStopped(t *testing.T) {
	for _, step := range []string{"prepare", "up", "seed", "down", "settle"} {
		r := newSetupRig(t, true)
		r.fail[step] = errors.New(step + " broke")
		_ = setupWith(context.Background(), r.rt, r.steps)
		if contains(r.events, "seal") {
			t.Errorf("%s failed: a half-built state must never be sealed: %v", step, r.events)
		}
	}
}

func TestBaselineCommandsFailClearlyWithoutASealedBaseline(t *testing.T) {
	codespaceRepo(t)
	for _, args := range [][]string{{"codespace", "baseline"}, {"codespace", "baseline", "verify", "--deep"}, {"codespace", "reset", "--yes"}} {
		var out bytes.Buffer
		err := run(args, &out)
		if !errors.Is(err, codespace.ErrNoBaseline) || !strings.Contains(err.Error(), "one-time setup") {
			t.Errorf("run(%v) = %v, want the instruction to run the setup once", args, err)
		}
	}
}

func TestBaselineCommandRejectsAnUnknownActionAndSealRefusesMissingState(t *testing.T) {
	codespaceRepo(t)
	var out bytes.Buffer
	if err := run([]string{"codespace", "baseline", "rebuild"}, &out); err == nil || !strings.Contains(err.Error(), "usage: ghostctl codespace") {
		t.Fatalf("err = %v", err)
	}
	if err := run([]string{"codespace", "baseline", "seal"}, &out); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("sealing nothing must fail: %v", err)
	}
}

func TestBaselineSealAndVerifyOverALiveCopy(t *testing.T) {
	codespaceRepo(t)
	rt, closeFlow, err := loadCodespace(&bytes.Buffer{}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFlow()
	home := rt.Cfg.Layout.Dir()
	for _, p := range rt.Baseline.Paths {
		full := filepath.Join(home, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/data") || strings.HasSuffix(p, "/tx") || strings.HasSuffix(p, "cases") || strings.HasSuffix(p, "replay-events") {
			if err := os.MkdirAll(filepath.Join(full, "case1"), 0o755); err != nil {
				t.Fatal(err)
			}
			full = filepath.Join(full, "case1", "f")
		} else if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// the case manifests are checked when sealing: write valid ones
	for slot, held := range map[string]string{"case1": "h1", "case2": "h2"} {
		dir := filepath.Join(rt.Ops.Paths.CaseDir(slot))
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"events":[{"event":{"replay_position":1}}],"held_out_event":{"event_id":"`+held+`","replay_position":2}}`), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"held_out_event_id":"`+held+`"}`), 0o644)
	}
	var out bytes.Buffer
	if err := run([]string{"codespace", "baseline", "seal"}, &out); err != nil || !strings.Contains(out.String(), "sealed") {
		t.Fatalf("seal: %v %s", err, out.String())
	}
	out.Reset()
	for _, args := range [][]string{{"codespace", "baseline"}, {"codespace", "baseline", "verify", "--deep"}} {
		if err := run(args, &out); err != nil || !strings.Contains(out.String(), "baseline ok") {
			t.Fatalf("run(%v): %v %s", args, err, out.String())
		}
	}
}
