package codespace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

func TestRecordProvesTheLearningContinuation(t *testing.T) {
	rr := newRecorder(t)
	var said []string
	rr.rec.Ops.Log = func(f string, a ...any) { said = append(said, fmt.Sprintf(f, a...)) }
	if err := rr.rec.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rr.probe.calls[SlotCase2] == 0 {
		t.Fatal("the later case must be checked for the carried knowledge")
	}
	all := strings.Join(said, "|")
	for _, want := range []string{"stamped at or before Event N", "Closest past lesson: Corrected inference: wait (candidate), similarity 0.72",
		"pre-check on the state before Event N", "were used: a candidate cites one"} {
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

func TestRecordRequiresTheLaterEpisodeToScoreTheCarriedKnowledge(t *testing.T) {
	rr := newRecorder(t)
	other := closestRecord(knowledge.DecisionApplicable, 0.9)
	other.Candidates[0].KnowledgeID = "k0" // scored something, but not what case 1 learned
	rr.probe.retrieval["run-man-2"] = other
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "scored none of the 1 entries learned earlier") {
		t.Fatalf("err = %v", err)
	}
	if len(rr.player.played) != 2 || len(rr.human.acted) != 1 {
		t.Fatalf("it fails right after the later Play, before spending on the human path of case 2: played=%v acted=%v", rr.player.played, rr.human.acted)
	}
}

// "Used" is reported, never asserted: a lesson that is offered and not cited is a plain log line, not a failed record.
func TestRecordReportsWhetherTheOfferedLessonWasUsedWithoutAssertingIt(t *testing.T) {
	rr := newRecorder(t)
	var said []string
	rr.rec.Ops.Log = func(f string, a ...any) { said = append(said, fmt.Sprintf(f, a...)) }
	rr.probe.used["run-man-2"] = nil // offered, and no candidate cites it
	if err := rr.rec.Run(context.Background()); err != nil {
		t.Fatalf("an offered lesson that was not used is reported, not a failure: %v", err)
	}
	if !strings.Contains(strings.Join(said, "|"), "were NOT used: no candidate cites one (reported, not asserted)") {
		t.Fatalf("not used must be logged plainly: %v", said)
	}
}

// Below the threshold nothing is offered, and the continuation is NOT demonstrated: HAR-129 step 32 needs a later episode
// where the learned knowledge is applicable. The record fails and says why, unless the owner opts out explicitly.
func belowThresholdRig(t *testing.T) (*recorderRig, *[]string) {
	rr := newRecorder(t)
	said := &[]string{}
	rr.rec.Ops.Log = func(f string, a ...any) { *said = append(*said, fmt.Sprintf(f, a...)) }
	rec := closestRecord(knowledge.DecisionBelowThreshold, 0.31)
	rec.Candidates[0].Matched = []knowledge.FeatureResult{{ID: "stage", Lesson: "negotiation", Case: "negotiation", Agreement: 1, Weight: 2}}
	rec.Candidates[0].Differs = []knowledge.FeatureResult{{ID: "motion", Lesson: "expansion", Case: "renewal", Weight: 2}}
	rec.Candidates[0].Ignored = []string{"blockers not_exists"}
	rr.probe.retrieval["run-man-2"] = rec
	rr.probe.offered["run-man-2"], rr.probe.used["run-man-2"] = nil, nil
	return rr, said
}

func TestRecordFailsWhenNoLessonReachesTheThresholdInTheLaterCase(t *testing.T) {
	rr, said := belowThresholdRig(t)
	err := rr.rec.Run(context.Background())
	if err == nil {
		t.Fatal("no applicable lesson in the later case must fail the record by default")
	}
	for _, want := range []string{"continuation not demonstrated", "closest lesson Corrected inference: wait (candidate)", "similarity 0.31",
		"threshold 0.60", "matched on: stage: negotiation", "differs on: motion: lesson expansion, case renewal",
		"reason: below the threshold", "not scored: blockers not_exists", "GHOST_DEMO_ALLOW_NO_CONTINUATION=1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if !strings.Contains(strings.Join(*said, "|"), "No similar knowledge in the knowledge base (closest: Corrected inference: wait, similarity 0.31") {
		t.Fatalf("the product's own statement is still surfaced: %v", *said)
	}
}

func TestRecordMayOptOutOfTheContinuationExplicitly(t *testing.T) {
	rr, said := belowThresholdRig(t)
	rr.rec.AllowNoContinuation = true
	if err := rr.rec.Run(context.Background()); err != nil {
		t.Fatalf("the owner's opt-out accepts the honest no-similar-knowledge outcome: %v", err)
	}
	all := strings.Join(*said, "|")
	if !strings.Contains(all, "continuation not demonstrated") || !strings.Contains(all, "GHOST_DEMO_ALLOW_NO_CONTINUATION=1") ||
		!strings.Contains(all, "none was offered and none could be used") {
		t.Fatalf("the opt-out is recorded in the log, never silent: %s", all)
	}
}

func TestTheContinuationOptOutIsOnlyTheExactEnvValueOne(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "": false, "0": false, "true": false, "yes": false} {
		if got := AllowNoContinuationFromEnv(func(string) string { return v }); got != want {
			t.Errorf("GHOST_DEMO_ALLOW_NO_CONTINUATION=%q -> %v", v, got)
		}
	}
}

func TestRecordRequiresTheClosestLessonToBePersistedAndAnAboveThresholdOneToBeOffered(t *testing.T) {
	rr := newRecorder(t)
	delete(rr.probe.retrieval, "run-man-2")
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "closest lesson and its score were not persisted") {
		t.Fatalf("err = %v", err)
	}
	rr = newRecorder(t)
	rr.probe.offered["run-man-2"] = nil // scored 0.72 at or above 0.60, and not offered
	err = rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "at or above 0.60, but was not offered") {
		t.Fatalf("err = %v", err)
	}
}

// The pre-check prints the similarity before the later case is played, and a probe that cannot estimate never blocks.
func TestRecordPrintsThePreCheckSimilarityBeforePlayingAndSurvivesAFailedEstimate(t *testing.T) {
	rr := newRecorder(t)
	rr.probe.closeness = nil
	var said []string
	rr.rec.Ops.Log = func(f string, a ...any) { said = append(said, fmt.Sprintf(f, a...)) }
	if err := rr.rec.Run(context.Background()); err != nil {
		t.Fatalf("a failed estimate must not block: %v", err)
	}
	if !strings.Contains(strings.Join(said, "|"), "could not estimate the similarity of the carried lessons before playing: no estimate") {
		t.Fatalf("log = %v", said)
	}
}

func TestRecordNeedsAProbeAndSurfacesProbeFailures(t *testing.T) {
	rr := newRecorder(t)
	rr.rec.Probe = nil
	if err := rr.rec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "knowledge probe") {
		t.Fatalf("err = %v", err)
	}
	for _, on := range []string{"", "used", "clock", "retrieval"} {
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

// A carried entry the matcher would never offer (a candidate that is not a narrow one, a scope too broad) is caught before the later
// case is played: its Play is about 135 thinking-model calls, and the old check found out only afterwards.
func TestRecordRefusesToPlayTheLaterCaseWhenTheCarriedKnowledgeCannotBeOffered(t *testing.T) {
	rr := newRecorder(t)
	rr.probe.pending[SlotCase1] = []FormedKnowledge{{ID: "k1", Status: "candidate", CreatedAt: eventN1,
		NotOffered: "a candidate with no supporting episode"}}
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "would not be offered") || !strings.Contains(err.Error(), "no supporting episode") {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Join(*rr.events, ","); strings.Contains(got, "play:man-2") {
		t.Fatalf("the later case must not be played: %s", got)
	}
}
