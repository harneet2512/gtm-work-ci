package codespace

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// fakePlayer releases Event N the way core does: the case's manifest reads "released" afterwards.
type fakePlayer struct {
	played []string
	fail   error
	noRun  bool
	events *[]string
	plat   *fakePlatform
}

func (p *fakePlayer) Play(_ context.Context, st demorun.DemoState) (demorun.DemoState, error) {
	p.played = append(p.played, st.ManifestID)
	*p.events = append(*p.events, "play:"+st.ManifestID)
	if p.fail != nil {
		return st, p.fail
	}
	p.plat.w.release(st.OpportunityID)
	if !p.noRun {
		st.RunID, st.EpisodeID = "run-"+st.ManifestID, "ep-"+st.ManifestID
	}
	return st, nil
}

// fakeHuman stands in for the Slack path: acting makes the knowledge form, as the real correction does.
type fakeHuman struct {
	events  *[]string
	probe   *fakeProbe
	acted   []string
	scripts []Script
	fail    error
	forms   bool // the correction becomes knowledge (case 1 only: the last case learns nothing it needs)
}

func (h *fakeHuman) Act(_ context.Context, c Case, st demorun.DemoState, s Script) (HumanPath, error) {
	*h.events = append(*h.events, "act:"+c.Slot)
	h.acted = append(h.acted, st.RunID)
	h.scripts = append(h.scripts, s)
	if h.fail != nil {
		return HumanPath{}, h.fail
	}
	if h.forms && c.Slot == SlotCase1 {
		h.probe.reveal(SlotCase1)
	}
	return HumanPath{Case: c.Label, ChoiceButton: "Select B", ChoiceTitle: "second", ChoiceRank: s.ChoiceRank, Subject: "Re: second", Body: "Hello",
		Correction: s.Correction}, nil
}

// replayClock of each case's Event N, years before the wall clock.
var (
	eventN1 = time.Date(2023, 11, 9, 9, 0, 0, 0, time.UTC)
	eventN2 = time.Date(2023, 11, 29, 9, 0, 0, 0, time.UTC)
)

// fakeProbe is what learning did, per case slot and per run.
type fakeProbe struct {
	formed    map[string][]FormedKnowledge
	pending   map[string][]FormedKnowledge // knowledge that appears when the human has corrected (reveal)
	retrieved map[string][]string
	used      map[string][]string
	offered   map[string][]string            // the knowledge the run judged applicable
	retrieval map[string]knowledge.Retrieval // the run's persisted closest-match record
	closeness *knowledge.Retrieval           // the pre-play estimate; nil: the probe cannot say
	clock     map[string]time.Time
	calls     map[string]int
	hideFor   int // Formed answers nothing new for the first hideFor calls on case 1 after the reveal: knowledge forms asynchronously
	revealed  bool
	err       error
	errOn     string // "used" or "clock": only that read fails
}

func (p *fakeProbe) reveal(slot string) {
	p.revealed = true
	p.formed[slot] = append(append([]FormedKnowledge(nil), p.formed[slot]...), p.pending[slot]...)
}

func (p *fakeProbe) Formed(_ context.Context, c Case) ([]FormedKnowledge, error) {
	p.calls[c.Slot]++
	if p.err != nil && p.errOn == "" {
		return nil, p.err
	}
	if c.Slot == SlotCase1 && p.revealed && p.hideFor > 0 {
		p.hideFor--
		return nil, nil
	}
	return p.formed[c.Slot], nil
}

func (p *fakeProbe) Retrieved(_ context.Context, _ Case, runID string) ([]string, error) {
	return p.retrieved[runID], nil
}

func (p *fakeProbe) Used(_ context.Context, _ Case, runID string) ([]string, error) {
	if p.errOn == "used" {
		return nil, p.err
	}
	return p.used[runID], nil
}

func (p *fakeProbe) Applicable(_ context.Context, _ Case, runID string) ([]string, error) {
	return p.offered[runID], nil
}

func (p *fakeProbe) Retrieval(_ context.Context, _ Case, runID string) (knowledge.Retrieval, error) {
	if p.errOn == "retrieval" {
		return knowledge.Retrieval{}, p.err
	}
	rv, ok := p.retrieval[runID]
	if !ok {
		return knowledge.Retrieval{}, errors.New("no closest-match record")
	}
	return rv, nil
}

func (p *fakeProbe) Closeness(_ context.Context, _ Case, _ []FormedKnowledge) (knowledge.Retrieval, error) {
	if p.closeness == nil {
		return knowledge.Retrieval{}, errors.New("no estimate")
	}
	return *p.closeness, nil
}

func (p *fakeProbe) ReplayClock(_ context.Context, _ Case, runID string) (time.Time, error) {
	if p.errOn == "clock" {
		return time.Time{}, p.err
	}
	return p.clock[runID], nil
}

// closestRecord is the persisted closest-match record of a run whose closest lesson, k1, scored the given similarity.
func closestRecord(decision string, score float64) knowledge.Retrieval {
	msg := "Closest past lesson: Corrected inference: wait (candidate), similarity 0.72, matched on stage; differs on motion."
	if decision != knowledge.DecisionApplicable {
		msg = "No similar knowledge in the knowledge base (closest: Corrected inference: wait, similarity 0.31 — below 0.60)."
	}
	return knowledge.Retrieval{RulesVersion: "knowledge_similarity:v1", Threshold: 0.6, MinComparable: 2, Message: msg,
		Candidates: []knowledge.Candidate{{KnowledgeID: "k1", Title: "Corrected inference: wait", Status: "candidate", Score: score, Decision: decision}}}
}

func healthyProbe() *fakeProbe {
	k := FormedKnowledge{ID: "k1", Status: "candidate", CreatedAt: eventN1}
	return &fakeProbe{
		calls:     map[string]int{},
		formed:    map[string][]FormedKnowledge{},
		pending:   map[string][]FormedKnowledge{SlotCase1: {k}},
		retrieved: map[string][]string{"run-man-2": {"k0", "k1"}},
		used:      map[string][]string{"run-man-2": {"k1"}},
		offered:   map[string][]string{"run-man-2": {"k1"}},
		retrieval: map[string]knowledge.Retrieval{"run-man-2": closestRecord(knowledge.DecisionApplicable, 0.72)},
		closeness: func() *knowledge.Retrieval { r := closestRecord(knowledge.DecisionApplicable, 0.72); return &r }(),
		clock:     map[string]time.Time{"run-man-1": eventN1, "run-man-2": eventN2},
	}
}

type recorderRig struct {
	rec    Recorder
	rig    *rig
	human  *fakeHuman
	player *fakePlayer
	probe  *fakeProbe
	events *[]string
	sheets []RunSheet
}

func newRecorder(t *testing.T) *recorderRig {
	t.Helper()
	r := newRig(t)
	r.seeded()
	events := &[]string{}
	probe := healthyProbe()
	rr := &recorderRig{rig: r, events: events, probe: probe}
	rr.human = &fakeHuman{events: events, probe: probe, forms: true}
	rr.player = &fakePlayer{events: events, plat: r.plat}
	rr.rec = Recorder{Ops: r.ops, Player: rr.player, Human: rr.human, Probe: probe, Script: DefaultScript(),
		Sleep: func(context.Context, time.Duration) error { return nil },
		Sheet: func(s RunSheet) error { rr.sheets = append(rr.sheets, s); return nil }}
	rr.rec.Ops.Carry = func(_ context.Context, from, to Case) (int, error) {
		*events = append(*events, "carry:"+from.Slot+"->"+to.Slot)
		probe.formed[to.Slot] = append([]FormedKnowledge(nil), probe.formed[from.Slot]...)
		return len(probe.formed[from.Slot]), nil
	}
	return rr
}

func TestRecordPlaysBothCasesInOneChronologyThroughTheSameHandoffAsThePlayButton(t *testing.T) {
	rr := newRecorder(t)
	if err := rr.rec.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// a restore of the baseline (at the start and the end; neither processes or carries anything), then the demo's own order: Play, the
	// human's Slack path, the handoff that carries the knowledge, Play, the human's path again
	got := strings.Join(*rr.events, ",")
	want := "play:man-1,act:case1,carry:case1->case2,play:man-2,act:case2"
	if got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
	if active, _ := ReadMarker(rr.rig.ops.Paths.ActiveFile()); active != SlotCase1 {
		t.Fatalf("the chronology must end reset at the start (case 1): %q", active)
	}
	if len(rr.sheets) != 1 || len(rr.sheets[0].Paths) != 2 || rr.sheets[0].Paths[1].Case != "EcoLite Innovations" {
		t.Fatalf("the run sheet carries what the human did in each case: %+v", rr.sheets)
	}
	for _, s := range rr.human.scripts {
		if s.ChoiceRank != 2 {
			t.Fatalf("the script disagrees with Ghost: rank 2 is chosen, got %+v", s)
		}
	}
}

func TestRecordEntersTheLaterCaseOnlyThroughTheHandoff(t *testing.T) {
	rr := newRecorder(t)
	rr.rec.Ops.Carry = nil
	if err := rr.rec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "did not reach EcoLite Innovations through the carry") {
		t.Fatalf("without the carry the later case never receives the knowledge: %v", err)
	}
	rr = newRecorder(t)
	rr.rec.Player = silentPlayer{} // Event N of case 1 is never released, so the handoff refuses to move on
	if err := rr.rec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "has not been played") {
		t.Fatalf("err = %v", err)
	}
}

type silentPlayer struct{}

func (silentPlayer) Play(_ context.Context, st demorun.DemoState) (demorun.DemoState, error) {
	st.RunID, st.EpisodeID = "run-"+st.ManifestID, "ep-"+st.ManifestID
	return st, nil
}

func TestRecordStopsAndNamesTheCaseAndStepThatFailed(t *testing.T) {
	rr := newRecorder(t)
	rr.human.fail = errors.New("press Send: Cliff said the action did not go through")
	err := rr.rec.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "press Send") || !strings.Contains(err.Error(), "MedTech Advances") {
		t.Fatalf("err = %v", err)
	}
	rr = newRecorder(t)
	rr.player.fail = errors.New("play timed out")
	if err := rr.rec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "play timed out") {
		t.Fatalf("err = %v", err)
	}
	rr = newRecorder(t)
	rr.player.noRun = true
	if err := rr.rec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "without a published run") {
		t.Fatalf("err = %v", err)
	}
	rr = newRecorder(t)
	rr.rec.Ops.Carry = func(context.Context, Case, Case) (int, error) { return 0, errors.New("no knowledge db") }
	if err := rr.rec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "carry knowledge") {
		t.Fatalf("err = %v", err)
	}
	rr = newRecorder(t)
	rr.rec.Sheet = func(RunSheet) error { return errors.New("disk full") }
	if err := rr.rec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "write the run sheet: disk full") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordRefusesWithoutAHumanOrAnUnseededCase(t *testing.T) {
	rr := newRecorder(t)
	rr.rec.Human = nil
	if err := rr.rec.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "needs a human") {
		t.Fatalf("err = %v", err)
	}
	fresh := newRig(t)
	fresh.seeded()
	_ = os.Remove(fresh.ops.Paths.State(SlotCase2))
	rec2 := Recorder{Ops: fresh.ops, Player: &fakePlayer{events: &[]string{}, plat: fresh.plat}, Human: rr.human, Probe: healthyProbe()}
	if err := rec2.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "never seeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordActivatesCaseOneWhenTheResetLeftAnotherActive(t *testing.T) {
	rr := newRecorder(t)
	// enter() trusts the reset; if a marker names another case it activates case 1 itself
	if err := WriteMarker(rr.rig.ops.Paths.ActiveFile(), SlotCase2); err != nil {
		t.Fatal(err)
	}
	if err := rr.rec.enter(context.Background(), 0, rr.rig.ops.Cases[0], demorun.DemoState{}); err != nil {
		t.Fatal(err)
	}
	if active, _ := ReadMarker(rr.rig.ops.Paths.ActiveFile()); active != SlotCase1 {
		t.Fatalf("active = %q", active)
	}
	if err := rr.rec.enter(context.Background(), 1, rr.rig.ops.Cases[1], demorun.DemoState{ManifestID: "man-404"}); err == nil {
		t.Fatal("a later case entered from an unknown manifest must fail")
	}
}

func TestPickCandidateFallsBackToTheBestWhenTheRankDoesNotExist(t *testing.T) {
	cands := []slacksurface.StrategyCandidate{{CandidateID: "c3", Ranking: 3}, {CandidateID: "c1", Ranking: 1}, {CandidateID: "c2", Ranking: 2}}
	c, pos, err := pickCandidate(cands, 9)
	if err != nil || c.CandidateID != "c1" || pos != 1 {
		t.Fatalf("pick = %+v %d %v", c, pos, err)
	}
	if c, pos, _ := pickCandidate(cands, 3); c.CandidateID != "c3" || pos != 0 {
		t.Fatalf("rank 3 = %s at %d", c.CandidateID, pos)
	}
	if _, _, err := pickCandidate(nil, 1); err == nil {
		t.Fatal("no candidates must be an error")
	}
}

func strategiesWithBlocked(blocked ...string) slacksurface.RunStrategies {
	rs := slacksurface.RunStrategies{}
	for i, id := range []string{"c1", "c2", "c3"} {
		rs.StrategySet.Candidates = append(rs.StrategySet.Candidates,
			slacksurface.StrategyCandidate{CandidateID: id, Ranking: i + 1, EvalBundleRef: "b-" + id})
		b := slacksurface.EvalBundle{ID: "b-" + id, StrategyCandidateID: id, Items: []slacksurface.EvalItem{
			{EvalType: "provenance", Verdict: slacksurface.VerdictPass, Result: &slacksurface.EvalResult{Blocking: true}}}}
		for _, bl := range blocked {
			if bl == id {
				b.Items = append(b.Items, slacksurface.EvalItem{EvalType: "quote_provenance", Verdict: slacksurface.VerdictFail,
					Result: &slacksurface.EvalResult{Blocking: true, Reason: "quote not in the activity"}})
			}
		}
		rs.EvalBundles = append(rs.EvalBundles, b)
	}
	return rs
}

func TestPickSendableSkipsABlockedRankTwoAndTakesTheNextSendable(t *testing.T) {
	rs := strategiesWithBlocked("c2")
	pick, pos, skipped, err := pickSendable(rs, 2)
	if err != nil || pick.CandidateID != "c3" || pos != 2 {
		t.Fatalf("pick = %+v %d %v", pick, pos, err)
	}
	if len(skipped) != 1 || skipped[0].CandidateID != "c2" || !strings.Contains(skipped[0].Why, "quote_provenance") {
		t.Fatalf("the skipped candidate must be named with its eval: %+v", skipped)
	}
}

func TestPickSendableKeepsTheDisagreementWhenTwoAreSendable(t *testing.T) {
	pick, _, skipped, err := pickSendable(strategiesWithBlocked("c3"), 2)
	if err != nil || pick.CandidateID != "c2" || len(skipped) != 1 {
		t.Fatalf("pick = %s skipped = %v err = %v", pick.CandidateID, skipped, err)
	}
}

func TestPickSendableFallsBackToTheOnlySendableCandidate(t *testing.T) {
	pick, _, skipped, err := pickSendable(strategiesWithBlocked("c2", "c3"), 2)
	if err != nil || pick.CandidateID != "c1" || len(skipped) != 2 {
		t.Fatalf("pick = %s skipped = %v err = %v", pick.CandidateID, skipped, err)
	}
}

func TestPickSendableRefusesWhenEveryCandidateIsBlocked(t *testing.T) {
	_, _, _, err := pickSendable(strategiesWithBlocked("c1", "c2", "c3"), 2)
	if err == nil || !strings.Contains(err.Error(), "quote_provenance") {
		t.Fatalf("err = %v", err)
	}
}
