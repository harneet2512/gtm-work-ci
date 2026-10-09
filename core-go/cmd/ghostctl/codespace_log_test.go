package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/codespace"
)

func TestCodespaceLogKeepsProgressAndTheFailureReasonInAFile(t *testing.T) {
	home := t.TempDir()
	var stdout bytes.Buffer
	out, finish := codespaceLog(home, "record", &stdout)
	out.Write([]byte("MedTech: playing Event N\n"))
	finish(errors.New("record MedTech: the gates were not all persisted within 15m0s (missing: D1)"))
	b, err := os.ReadFile(filepath.Join(home, "logs", "codespace-record.log"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"started", "MedTech: playing Event N", "FAILED", "not all persisted within 15m0s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log lacks %q:\n%s", want, got)
		}
	}
	if !strings.Contains(stdout.String(), "playing Event N") {
		t.Fatal("stdout still receives the output")
	}
}

func TestCodespaceLogWorksWithoutAStdoutAndRecordsSuccess(t *testing.T) {
	home := t.TempDir()
	out, finish := codespaceLog(home, "up", nil)
	out.Write([]byte("control: healthy\n"))
	finish(nil)
	b, _ := os.ReadFile(filepath.Join(home, "logs", "codespace-up.log"))
	if !strings.Contains(string(b), "control: healthy") || !strings.Contains(string(b), "finished") {
		t.Fatalf("log: %s", b)
	}
}

func TestCodespaceLogLeavesOtherCommandsAndAnUnsetHomeAlone(t *testing.T) {
	var stdout bytes.Buffer
	if out, _ := codespaceLog("", "record", &stdout); out != &stdout {
		t.Fatal("no home: plain writer")
	}
	home := t.TempDir()
	if out, _ := codespaceLog(home, "status", &stdout); out != &stdout {
		t.Fatal("status prints JSON and is not logged")
	}
	if _, err := os.Stat(filepath.Join(home, "logs")); err == nil {
		t.Fatal("nothing should be created for an unlogged command")
	}
}

func TestCodespaceDriveIsLoggedLikeTheOtherUnattendedCommands(t *testing.T) {
	home := t.TempDir()
	var stdout bytes.Buffer
	out, finish := codespaceLog(home, "drive", &stdout)
	out.Write([]byte("driving case1\n"))
	finish(nil)
	b, err := os.ReadFile(filepath.Join(home, "logs", "codespace-drive.log"))
	if err != nil || !strings.Contains(string(b), "driving case1") || !strings.Contains(string(b), "finished") {
		t.Fatalf("drive log: %s %v", b, err)
	}
}

func TestCodespaceDriveNeedsExactlyOneCaseAndSlackOn(t *testing.T) {
	for _, args := range [][]string{nil, {"case1", "case2"}} {
		if err := codespaceDrive(context.Background(), codespace.Runtime{}, args, io.Discard); err == nil || !strings.Contains(err.Error(), "drive <case1|case2>") {
			t.Fatalf("args %v: err = %v", args, err)
		}
	}
	rt := codespace.Runtime{}
	rt.Cfg.NoSlack = true
	if err := codespaceDrive(context.Background(), rt, []string{"case1"}, io.Discard); err == nil {
		t.Fatal("a drive with Slack off must be refused")
	}
}
