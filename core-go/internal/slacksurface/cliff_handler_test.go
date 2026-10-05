package slacksurface

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Cliff's button handlers (HAR-129 FINAL DEMO SURFACES): Select updates the message in place, Don't learn this is a
// no-learning verdict, Edit interpretation stores a correction plus a note, and Message 1 gains View trace once
// its episode exists, without ever becoming a fourth message.

func newWebHandler() (*Handler, *memCore, *fakePoster) {
	core, poster := newMemCore(), &fakePoster{}
	return NewHandler(core, poster, "C0TEST", testWeb, quietLog()), core, poster
}

// answered is the state after the human sent: the inference is ready and still pending.
func answered(t *testing.T) (*Handler, *memCore, *fakePoster) {
	t.Helper()
	h, core, poster := newWebHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	runWork(t, h, actionCB(ActionSelectedSend, "T2", tgtRun))
	h.Wait()
	return h, core, poster
}

func lastUpdate(t *testing.T, p *fakePoster) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.updates) == 0 {
		t.Fatal("no message was updated")
	}
	return mustJSON(p.updates[len(p.updates)-1])
}

func lastVerdictWrite(core *memCore) string {
	var last string
	for _, w := range core.writes() {
		if strings.HasPrefix(w, "judgment-verdict ") {
			last = strings.TrimPrefix(w, "judgment-verdict ")
		}
	}
	return last
}

func TestSelectingAnOptionUpdatesTheSameMessageAndPostsNothing(t *testing.T) {
	h, core, poster := newWebHandler()

	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))

	if len(poster.posts) != 0 || len(poster.updates) != 1 || poster.updateTS[0] != "1.2" {
		t.Fatalf("posts=%d updates=%d at %v: Select must update the chooser's own message, not post", len(poster.posts), len(poster.updates), poster.updateTS)
	}
	raw := mustJSON(poster.updates[0])
	for _, want := range []string{"You selected B.", "Inspect evals", "?candidate=" + fixtureCandB, ActionSelectedEdit, ActionSelectedSend} {
		if !strings.Contains(raw, want) {
			t.Errorf("the expanded message lacks %q", want)
		}
	}
	if w := core.writes(); len(w) != 1 || strings.HasPrefix(w[0], "send") {
		t.Fatalf("writes = %v: selecting never sends", w)
	}
}

func TestDontLearnThisSubmitsANoLearningVerdictAndShowsIt(t *testing.T) {
	h, core, poster := answered(t)
	opened := len(poster.opened)

	runWork(t, h, actionCB(ActionJudgmentNoLearn, "T3", tgtJ))

	got := lastVerdictWrite(core)
	if !strings.Contains(got, `"verdict":"no_learning"`) || !strings.Contains(got, `"surface":"slack"`) || !strings.Contains(got, `"actor_label":"slack:U1 (alex)"`) {
		t.Fatalf("verdict write = %s, want an explicit no_learning verdict from slack", got)
	}
	if strings.Contains(got, "corrected_statement") || strings.Contains(got, `"note"`) {
		t.Fatalf("a no-learning verdict carries no correction or note: %s", got)
	}
	if core.inf.HumanVerdict != VerdictNoLearning {
		t.Fatalf("core holds %q", core.inf.HumanVerdict)
	}
	if raw := lastUpdate(t, poster); !strings.Contains(raw, "Not learning from this") || strings.Contains(raw, ActionJudgmentNoLearn) {
		t.Fatalf("Message 3 must show the outcome and drop the buttons: %s", raw)
	}
	if len(poster.opened) != opened || len(poster.ephemerals) != 0 {
		t.Fatalf("no modal and no error for a plain verdict: opened=%d ephemerals=%v", len(poster.opened)-opened, poster.ephemerals)
	}
}

func TestACorrectAfterDontLearnIsRefusedAndTheMessageShowsWhatCoreHolds(t *testing.T) {
	h, core, poster := answered(t)
	runWork(t, h, actionCB(ActionJudgmentNoLearn, "T3", tgtJ))

	runWork(t, h, actionCB(ActionJudgmentConfirm, "T4", tgtJ))

	if core.inf.HumanVerdict != VerdictNoLearning {
		t.Fatalf("core holds %q: a refused Correct changed the verdict", core.inf.HumanVerdict)
	}
	if raw := lastUpdate(t, poster); !strings.Contains(raw, "Not learning from this") {
		t.Fatalf("the message must be re-rendered from core's record: %s", raw)
	}
	if len(poster.ephemerals) != 1 || !strings.Contains(poster.ephemerals[0], "earlier answer") {
		t.Fatalf("only the clicker is told why: %v", poster.ephemerals)
	}
}

func TestCorrectStillConfirmsWithoutAModal(t *testing.T) {
	h, core, poster := answered(t)
	opened := len(poster.opened)

	runWork(t, h, actionCB(ActionJudgmentConfirm, "T3", tgtJ))

	if !strings.Contains(lastVerdictWrite(core), `"verdict":"confirmed"`) || core.inf.HumanVerdict != VerdictConfirmed || len(poster.opened) != opened {
		t.Fatalf("Correct is a one-click confirm: %s", lastVerdictWrite(core))
	}
}

func TestEditInterpretationOpensAModalPrefilledWithTheInterpretation(t *testing.T) {
	h, core, poster := answered(t)

	runWork(t, h, actionCB(ActionJudgmentCorrect, "T3", tgtJ))

	if len(poster.views) != 1 {
		t.Fatalf("modals filled = %d", len(poster.views))
	}
	raw := mustJSON(poster.views[0])
	if poster.views[0].CallbackID != CallbackCorrectionModal || !strings.Contains(raw, "Edit interpretation") ||
		!strings.Contains(raw, strings.ReplaceAll(core.inf.InferredSemanticDelta.Statement, `"`, `\"`)) {
		t.Fatalf("modal = %s", raw)
	}
	for _, w := range core.writes() {
		if strings.HasPrefix(w, "judgment-verdict") {
			t.Fatalf("opening the modal wrote %s", w)
		}
	}
}

func TestSavingAnEditedInterpretationStoresACorrectionPlusANoteInOneWrite(t *testing.T) {
	h, core, poster := answered(t)
	meta := Target{EpisodeID: FixtureEpisodeID, RunID: FixtureRunID, ChannelID: "C0TEST", MessageTS: "9.9"}

	ack := runWork(t, h, viewCB("V9", CallbackCorrectionModal, meta, map[string]string{
		inputCorrection: "Not timing: Marco is new to us and wants to read first.", inputNote: "only for security-led reviews"}))

	if ack != nil {
		t.Fatalf("a valid edit closes the modal: %v", mustJSON(ack))
	}
	var verdicts int
	for _, w := range core.writes() {
		if strings.HasPrefix(w, "judgment-verdict") {
			verdicts++
		}
	}
	got := lastVerdictWrite(core)
	if verdicts != 1 || !strings.Contains(got, `"verdict":"corrected"`) ||
		!strings.Contains(got, `"corrected_statement":"Not timing: Marco is new to us and wants to read first."`) ||
		!strings.Contains(got, `"note":"only for security-led reviews"`) {
		t.Fatalf("%d verdict writes, last %s: want one corrected verdict carrying the edit and the note", verdicts, got)
	}
	if raw := lastUpdate(t, poster); !strings.Contains(raw, "Corrected:") || !strings.Contains(raw, "Marco is new to us") || !strings.Contains(raw, "only for security-led reviews") {
		t.Fatalf("Message 3 shows the correction and its note: %s", raw)
	}
}

func TestSavingTheInterpretationUnchangedIsRefusedInline(t *testing.T) {
	h, core, _ := answered(t)
	meta := Target{EpisodeID: FixtureEpisodeID, RunID: FixtureRunID, ChannelID: "C0TEST", MessageTS: "9.9"}
	before := len(core.writes())

	ack := runWork(t, h, viewCB("V9", CallbackCorrectionModal, meta, map[string]string{
		inputCorrection: "  " + core.inf.InferredSemanticDelta.Statement + " "}))

	if ack == nil || !strings.Contains(mustJSON(ack), "still my interpretation") {
		t.Fatalf("an unchanged interpretation is not a correction: %v", mustJSON(ack))
	}
	if len(core.writes()) != before {
		t.Fatal("an unchanged interpretation reached core")
	}
}

func TestMessageOneGainsViewTraceWhenItsEpisodeAppearsWithoutAFourthMessage(t *testing.T) {
	core, mc := newRefCore()
	sim := newSlackSim()
	// Message 1 is published first (no episode yet), then the strategy set of its episode.
	src := &memEvents{pending: []OutboxEvent{biEvent(1, mc), setEvent(2, mc, biOf(mc))}}
	pub := NewPublisher(core, sim, "C0TEST", testWeb)
	relay, err := NewRelay(src, pub, quietLog(), 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := relay.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}

	if sim.visible("bi") != 1 || sim.visible("chooser") != 1 || len(sim.msgs) != 2 {
		t.Fatalf("visible bi=%d chooser=%d of %d: the episode shows at most its three messages, exactly once each", sim.visible("bi"), sim.visible("chooser"), len(sim.msgs))
	}
	var m1 Message
	for _, m := range sim.msgs {
		if m.msg.Meta != nil && m.msg.Meta.Kind == KindBI {
			m1 = m.msg
		}
	}
	trace := buttonByAction(t, m1, ActionBIViewTrace)
	if trace.URL != "https://ghost.example.test/episodes/"+episodeOf(mc) {
		t.Fatalf("View trace = %s, want the episode of the strategy set", trace.URL)
	}
	if sim.updates[sim.msgs[0].ts] != 1 {
		t.Fatalf("updates = %v: Message 1 is refreshed in place once, with View trace", sim.updates)
	}
}
