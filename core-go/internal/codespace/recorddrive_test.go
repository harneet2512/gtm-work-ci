package codespace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

type stubPlayer struct {
	played []demorun.DemoState
	err    error
}

func (p *stubPlayer) Play(_ context.Context, st demorun.DemoState) (demorun.DemoState, error) {
	p.played = append(p.played, st)
	st.RunID, st.EpisodeID = "run-1", "ep-1"
	return st, p.err
}

type stubHuman struct {
	acted []demorun.DemoState
	err   error
}

func (h *stubHuman) Act(_ context.Context, c Case, st demorun.DemoState, s Script) (HumanPath, error) {
	h.acted = append(h.acted, st)
	return HumanPath{Case: c.Label, ChoiceRank: s.ChoiceRank}, h.err
}

func TestCaseOfSlotFindsTheCaseAndRefusesAnUnknownSlot(t *testing.T) {
	c, err := caseOfSlot(DefaultCases(), SlotCase2)
	if err != nil || c.Slot != SlotCase2 || c.Label == "" {
		t.Fatalf("case = %+v err = %v", c, err)
	}
	if c, err := caseOfSlot(DefaultCases(), "case9"); err == nil || !strings.Contains(err.Error(), `"case9"`) || !strings.Contains(err.Error(), SlotCase1) || c.Slot != "" {
		t.Fatalf("an unknown slot must be an error naming it and the known ones, got %+v %v", c, err)
	}
}

func TestSlackHumanOfRefusesAnyOtherHumanWithoutPanicking(t *testing.T) {
	if _, err := slackHumanOf(&stubHuman{}); err == nil || !strings.Contains(err.Error(), "Slack human") {
		t.Fatalf("a non-Slack human must be an error, got %v", err)
	}
	var nilHuman *SlackHuman
	if _, err := slackHumanOf(nilHuman); err == nil {
		t.Fatal("a nil Slack human must be an error")
	}
	if _, err := slackHumanOf(nil); err == nil {
		t.Fatal("no human must be an error")
	}
	want := &SlackHuman{}
	if got, err := slackHumanOf(want); err != nil || got != want {
		t.Fatalf("a Slack human is returned as is: %v %v", got, err)
	}
}

func TestDriveRefusesAnUnknownSlotBeforeTouchingAnything(t *testing.T) {
	t.Chdir(t.TempDir()) // the rig lays its demo home out relative to the working directory
	r := newAssembleRig(t)
	rt := Assemble(r.flow, r.options(slackOK))
	if _, _, err := rt.Drive(context.Background(), "case9", nil); err == nil || !strings.Contains(err.Error(), "unknown case") {
		t.Fatalf("err = %v", err)
	}
}

func TestDriveNamesACaseThatIsNotSeeded(t *testing.T) {
	t.Chdir(t.TempDir()) // the rig lays its demo home out relative to the working directory
	r := newAssembleRig(t)
	rt := Assemble(r.flow, r.options(slackOK))
	if _, _, err := rt.Drive(context.Background(), SlotCase1, nil); err == nil || !strings.Contains(err.Error(), "not seeded") {
		t.Fatalf("err = %v", err)
	}
}

// Drive reaches Play for a seeded case (the rig has nothing seeded for core, so demorun.RunPlay refuses and the error is its own).
func TestDriveSeededCaseGoesAsFarAsPlay(t *testing.T) {
	t.Chdir(t.TempDir()) // the rig lays its demo home out relative to the working directory
	r := newAssembleRig(t)
	rt := Assemble(r.flow, r.options(slackOK))
	if err := demorun.SaveState(rt.Ops.Paths.State(SlotCase1), demorun.DemoState{ManifestID: "man-1", OpportunityID: "opp-1", CaseName: "MedTech Advances"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := rt.Drive(context.Background(), SlotCase1, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "demorun:") {
		t.Fatalf("a seeded case must get past the checks and fail inside Play, got %v", err)
	}
}

func TestDriveCasePlaysThenActsThenReturnsThePlayedState(t *testing.T) {
	player, human := &stubPlayer{}, &stubHuman{}
	rec := Recorder{Player: player, Human: human, Script: DefaultScript()}
	c, _ := caseOfSlot(DefaultCases(), SlotCase1)
	played, path, err := rec.driveCase(context.Background(), c, demorun.DemoState{ManifestID: "man-1"})
	if err != nil || played.RunID != "run-1" || path.Case != c.Label || path.ChoiceRank != rec.Script.ChoiceRank {
		t.Fatalf("played = %+v path = %+v err = %v", played, path, err)
	}
	if len(player.played) != 1 || len(human.acted) != 1 || human.acted[0].RunID != "run-1" {
		t.Fatalf("the human must act on the state Play returned: %+v", human.acted)
	}
}

func TestDriveCaseStopsAtTheFirstFailure(t *testing.T) {
	c, _ := caseOfSlot(DefaultCases(), SlotCase1)
	boom := errors.New("boom")
	human := &stubHuman{}
	if _, _, err := (Recorder{Player: &stubPlayer{err: boom}, Human: human}).driveCase(context.Background(), c, demorun.DemoState{}); !errors.Is(err, boom) || len(human.acted) != 0 {
		t.Fatalf("a failed Play must stop before the human acts: %v %v", err, human.acted)
	}
	if _, _, err := (Recorder{Player: &stubPlayer{}, Human: &stubHuman{err: boom}}).driveCase(context.Background(), c, demorun.DemoState{}); !errors.Is(err, boom) {
		t.Fatalf("a failed human path must be returned: %v", err)
	}
}
