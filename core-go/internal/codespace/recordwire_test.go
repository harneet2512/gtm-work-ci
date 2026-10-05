package codespace

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

func TestCorePlayerRefusesAStateWhoseCaseHasNoDatabase(t *testing.T) {
	p := corePlayer{cases: DefaultCases(), dsnFor: func(Case) (string, error) { return "postgres://x", nil }}
	_, err := p.Play(context.Background(), demorun.DemoState{OpportunityID: "006Wt000007BZZZZZZZ"})
	if err == nil || !strings.Contains(err.Error(), "no case database") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRecorderDrivesTheRealSlackHandlersAndWritesTheRunSheetToTheDemoHome(t *testing.T) {
	r := newAssembleRig(t)
	rt := Assemble(r.flow, r.options(slackOK))
	rec := rt.NewRecorder()
	if rec.Script.ChoiceRank != 2 || rec.Script.UserID == "" || len(rec.Ops.Cases) != 2 {
		t.Fatalf("recorder = %+v", rec.Script)
	}
	human, ok := rec.Human.(*SlackHuman)
	if !ok || rt.Client.Token != "t0k" || human.Core == nil || human.WebURL != rt.Cfg.WebURL() {
		t.Fatalf("human = %T token set = %v", rec.Human, rt.Client.Token != "")
	}
	if err := rec.Sheet(RunSheet{Paths: []HumanPath{{Case: "MedTech Advances", ChoiceButton: "Select B"}}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(RunSheetPath(rt.Cfg.Layout.Dir()))
	if err != nil || !strings.Contains(string(b), "Select B") {
		t.Fatalf("the run sheet must be written under the demo home: %v %s", err, b)
	}
	if rt.Ops.Carry == nil {
		t.Fatal("activating a later case must carry what the earlier one learned")
	}
}

func TestRecordWithSlackOffIsExplicitAndSkipsTheSlackSecrets(t *testing.T) {
	r := newAssembleRig(t)
	r.flow.Cfg.Process = demorun.Merge(r.flow.Cfg.Process, demorun.Env{"GHOST_DEMO_NO_SLACK": "1"})
	rt := Assemble(r.flow, r.options(slackOK)) // the tokens exist, yet the bot must stay off
	if !rt.Cfg.NoSlack || contains(rt.Status.Required, "slackbot") {
		t.Fatalf("NoSlack=%v required=%v", rt.Cfg.NoSlack, rt.Status.Required)
	}
	if got := strings.Join(missingOf(rt), ","); got != "" {
		t.Fatalf("a record run without Slack must not ask for Slack secrets: %q", got)
	}
	if err := rt.CheckRecordable(); err != nil {
		t.Fatalf("Slack off must be recordable: %v", err)
	}
}

func TestRecordRefusesWhileSlackIsOnSoNothingLandsInTheDemoChannel(t *testing.T) {
	r := newAssembleRig(t)
	rt := Assemble(r.flow, r.options(slackOK))
	err := rt.CheckRecordable()
	if err == nil || !strings.Contains(err.Error(), "Slack") || !strings.Contains(err.Error(), "GHOST_DEMO_NO_SLACK") {
		t.Fatalf("err = %v", err)
	}
	if err := rt.Record(context.Background()); err == nil {
		t.Fatal("Record must refuse before it touches anything")
	}
}

func TestTheRecorderIsWiredWithTheDatabaseProbe(t *testing.T) {
	r := newAssembleRig(t)
	rec := Assemble(r.flow, r.options(slackOK)).NewRecorder()
	if _, ok := rec.Probe.(DBProbe); !ok {
		t.Fatalf("probe = %T", rec.Probe)
	}
}
