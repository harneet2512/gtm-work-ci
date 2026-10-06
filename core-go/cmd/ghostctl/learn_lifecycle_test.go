package main

// learn subcommand tests beyond `report`: the lifecycle verbs (status, backtest, advance, promote, retire)
// and the stream contract of `learn report` (JSON on stdout, the target-host line on the injected error
// writer). One embedded Postgres serves every leg.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

func TestLearnReportWritesTheHostLineToTheErrorWriterOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	t.Setenv("DATABASE_URL", env.URL)

	var out, errw bytes.Buffer
	if err := runLearnTo([]string{"report", "--all"}, &out, &errw); err != nil {
		t.Fatalf("learn report --all: %v", err)
	}
	if !strings.Contains(errw.String(), "target database host:") {
		t.Fatalf("host line missing from the error writer: %q", errw.String())
	}
	if strings.Contains(out.String(), "target database host") || !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Fatalf("stdout must be the JSON document only: %q", out.String())
	}

	// --- lifecycle verbs on the same database ---
	run := func(args ...string) (string, error) {
		var o, e bytes.Buffer
		err := runLearnTo(args, &o, &e)
		return o.String(), err
	}

	if got, err := run("status"); err != nil || !strings.Contains(got, "no evaluator versions registered") {
		t.Fatalf("empty status = %q, %v", got, err)
	}
	if _, err := run("status", "extra"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("status with an argument: %v", err)
	}
	if _, err := env.DB.Exec(`INSERT INTO evaluator_versions (evaluator, version, status, kind, rubric, created_from)
 VALUES ('cta_calibration', 9, 'candidate', 'semantic', 'x', 'manual')`); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	if got, err := run("status"); err != nil || !strings.Contains(got, "cta_calibration") || !strings.Contains(got, "candidate") {
		t.Fatalf("status = %q, %v", got, err)
	}

	// advance is gated on a passing backtest: none exists, so the gate refuses.
	if _, err := run("advance", "--evaluator", "cta_calibration", "--version", "9"); !errors.Is(err, learning.ErrGate) {
		t.Fatalf("advance without a backtest: %v, want ErrGate", err)
	}
	// promote and backtest fail closed on an unreadable rules file.
	missing := "--rules=" + t.TempDir() + "/no-such-rules.json"
	if _, err := run("promote", "--evaluator", "cta_calibration", "--version", "9", missing); err == nil {
		t.Fatal("promote with unreadable rules succeeded")
	}
	if _, err := run("backtest", "--evaluator", "cta_calibration", "--version", "9", missing); err == nil ||
		!strings.Contains(err.Error(), "load knowledge rules") {
		t.Fatalf("backtest with unreadable rules: %v", err)
	}
	// retire needs a reason, then records the transition, then refuses a second retirement.
	if _, err := run("retire", "--evaluator", "cta_calibration", "--version", "9"); err == nil ||
		!strings.Contains(err.Error(), "--reason") {
		t.Fatalf("retire without a reason: %v", err)
	}
	got, err := run("retire", "--evaluator", "cta_calibration", "--version", "9", "--reason", "superseded in test")
	if err != nil || !strings.Contains(got, "cta_calibration:v9 candidate -> retired") {
		t.Fatalf("retire = %q, %v", got, err)
	}
	if _, err := run("retire", "--evaluator", "cta_calibration", "--version", "9", "--reason", "again"); !errors.Is(err, learning.ErrGate) {
		t.Fatalf("second retire: %v, want ErrGate", err)
	}
	// Argument validation of the verbs that reach the database.
	for _, args := range [][]string{
		{"advance"},
		{"advance", "--evaluator", "cta_calibration"},
		{"advance", "--evaluator", "not_an_axis", "--version", "1"},
		{"promote", "--evaluator", "cta_calibration", "--version", "0"},
	} {
		if _, err := run(args...); err == nil {
			t.Fatalf("learn %v succeeded", args)
		}
	}
}
