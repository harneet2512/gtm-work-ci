package codespace

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRecordProvesTheLearningContinuation(t *testing.T) {
	rr := newRecorder(t)
	var said []string
	rr.rec.Ops.Log = func(f string, a ...any) { said = append(said, strings.TrimSpace(f)) }
	if err := rr.rec.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rr.probe.calls[SlotCase2] == 0 {
		t.Fatal("the later case must be checked for the carried knowledge")
	}
	all := strings.Join(said, "|")
	for _, want := range []string{"stamped at or before Event N", "retrieved and used knowledge learned earlier"} {
		if !strings.Contains(all, want) {
			t.Errorf("the run did not report %q: %s", want, all)
		}
	}
}

func TestRecordWaitsForTheKnowledgeToForm(t *testing.T) {
	rr := newRecorder(t)
	rr.probe.hideFor = 4
	if err := rr.rec.Run(context.Background()); err != nil {
		t.Fatalf("knowledge forms asynchronously after the verdict: %v", err)
	}
	rr2 := newRecorder(t)
	rr2.probe.hideFor = 1 << 20
	rr2.rec.Wait, rr2.rec.Poll = 6*time.Second, 3*time.Second
	err := rr2.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no knowledge formed from the human's correction in MedTech Advances") {
		t.Fatalf("err = %v", err)
	}
}

func TestOnlyKnowledgeFormedAfterTheHumanPathCounts(t *testing.T) {
	rr := newRecorder(t)
	old := FormedKnowledge{ID: "old", Status: "confirmed", CreatedAt: eventN1.Add(-48 * time.Hour)}
	rr.probe.formed[SlotCase1] = []FormedKnowledge{old}
	rr.rec.Ops.Carry = func(_ context.Context, from, to Case) (int, error) {
		rr.probe.formed[to.Slot] = append([]FormedKnowledge(nil), rr.probe.formed[from.Slot]...)
		return len(rr.probe.formed[to.Slot]), nil
	}
	rr.probe.retrieved["run-man-2"], rr.probe.used["run-man-2"] = []string{"k1"}, []string{"k1"}
	rr.human.forms = false // the correction formed nothing: the knowledge that was already there must not pass for it
	rr.rec.Wait, rr.rec.Poll = 6*time.Second, 3*time.Second
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no knowledge formed") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordRejectsKnowledgeStampedAfterTheEpisodesEventN(t *testing.T) {
	rr := newRecorder(t)
	// stamped with the database clock, years after the replay world: the wall-clock bug of HAR-144
	rr.probe.pending[SlotCase1] = []FormedKnowledge{{ID: "k1", Status: "candidate", CreatedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}}
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "after the episode's Event N") || !strings.Contains(err.Error(), "HAR-144") {
		t.Fatalf("err = %v", err)
	}
}

func TestKnowledgeStampedExactlyAtEventNIsAccepted(t *testing.T) {
	rr := newRecorder(t)
	rr.probe.pending[SlotCase1] = []FormedKnowledge{{ID: "k1", Status: "candidate", CreatedAt: eventN1}}
	if err := rr.rec.Run(context.Background()); err != nil {
		t.Fatalf("a stamp at Event N is at or before it: %v", err)
	}
}

func TestRecordRequiresTheCarryToLandInTheLaterCaseWithEachEntrysOwnTime(t *testing.T) {
	rr := newRecorder(t)
	rr.rec.Ops.Carry = func(_ context.Context, _, to Case) (int, error) { return 0, nil } // reported success, wrote nothing
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "k1 learned earlier did not reach EcoLite Innovations") {
		t.Fatalf("err = %v", err)
	}
	if len(rr.player.played) != 1 {
		t.Fatalf("the later case must not be played without the knowledge: %v", rr.player.played)
	}

	// carried, but every entry stamped with the latest time instead of its own
	rr = newRecorder(t)
	rr.rec.Ops.Carry = func(_ context.Context, from, to Case) (int, error) {
		for _, k := range rr.probe.formed[from.Slot] {
			k.CreatedAt = k.CreatedAt.Add(24 * time.Hour)
			rr.probe.formed[to.Slot] = append(rr.probe.formed[to.Slot], k)
		}
		return 1, nil
	}
	err = rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not its own replay time") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordRequiresCarriedKnowledgeToPredateTheLaterEventN(t *testing.T) {
	rr := newRecorder(t)
	rr.probe.clock["run-man-2"] = eventN1 // the later episode claims to be no later than what was learned
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not before EcoLite Innovations's Event N") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordRequiresTheLaterEpisodeToRetrieveTheKnowledge(t *testing.T) {
	rr := newRecorder(t)
	rr.probe.retrieved["run-man-2"] = []string{"k0"} // retrieved something, but not what case 1 learned
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "retrieved none of the 1 entries learned earlier") {
		t.Fatalf("err = %v", err)
	}
	if len(rr.player.played) != 2 || len(rr.human.acted) != 1 {
		t.Fatalf("it fails right after the later Play, before spending on the human path of case 2: played=%v acted=%v", rr.player.played, rr.human.acted)
	}
}

func TestRecordRequiresTheLaterEpisodeToUseTheKnowledgeNotOnlyRetrieveIt(t *testing.T) {
	rr := newRecorder(t)
	rr.probe.used["run-man-2"] = []string{"k0"} // a candidate cites other knowledge, none of what was carried
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "retrieved knowledge learned earlier but did not use it") {
		t.Fatalf("err = %v", err)
	}
	rr = newRecorder(t)
	rr.probe.used["run-man-2"] = nil
	if err := rr.rec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "did not use it") {
		t.Fatalf("an episode that used nothing must fail too: %v", err)
	}
}

func TestRecordNeedsAProbeAndSurfacesProbeFailures(t *testing.T) {
	rr := newRecorder(t)
	rr.rec.Probe = nil
	if err := rr.rec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "knowledge probe") {
		t.Fatalf("err = %v", err)
	}
	for _, on := range []string{"", "used", "clock"} {
		rr = newRecorder(t)
		rr.probe.err, rr.probe.errOn = errors.New("db down"), on
		if err := rr.rec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "db down") {
			t.Fatalf("probe failure on %q: err = %v", on, err)
		}
	}
}

func TestAFailedRecordPutsTheChronologyBackAtTheStart(t *testing.T) {
	rr := newRecorder(t)
	rr.human.fail = errors.New("Cliff refused the form")
	if err := rr.rec.Run(context.Background()); err == nil {
		t.Fatal("want the human path failure")
	}
	if active, _ := ReadMarker(rr.rig.ops.Paths.ActiveFile()); active != SlotCase1 {
		t.Fatalf("a failed record must not leave the demo in a played state: active=%q", active)
	}
	plat := rr.rig.plat.events
	if tail := strings.Join(plat[len(plat)-7:], ","); tail != "stop-core,stop-stores,start-stores,stop-core,start-core:case2,stop-core,start-core:case1" {
		t.Fatalf("tail = %v", plat)
	}
}
