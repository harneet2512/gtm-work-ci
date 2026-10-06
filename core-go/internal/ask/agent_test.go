package ask

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// hookWorker is a worker that runs a function per call, so a test can make tool calls through core as the real
// worker does and answer with whatever it likes.
type hookWorker struct {
	got []WorkerRequest
	fn  func(call int, req WorkerRequest) (WorkerAnswer, error)
}

func (h *hookWorker) Ask(_ context.Context, r WorkerRequest) (WorkerAnswer, error) {
	h.got = append(h.got, r)
	return h.fn(len(h.got), r)
}

func plainAnswer(text string) WorkerAnswer {
	return WorkerAnswer{AnswerMarkdown: text, DontKnow: strings.HasPrefix(text, "I don't know"), Model: "scripted"}
}

func ask(t *testing.T, s *Service, ref, text string) Answer {
	t.Helper()
	ans, err := s.Ask(context.Background(), Request{Text: text, ChannelKind: ChannelThread, User: "U1", ThreadRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	return ans
}

func TestAFollowUpSeesThePriorTurnsOfItsThreadAndOnlyOfItsThread(t *testing.T) {
	w := &hookWorker{fn: func(int, WorkerRequest) (WorkerAnswer, error) { return plainAnswer("MedTech is in Negotiation."), nil }}
	s := newService(t, w, medtech(), nil)
	ask(t, s, "C1:100", "What stage is MedTech in?")
	ask(t, s, "C1:100", "and EcoLite?")
	ask(t, s, "C1:200", "why?") // another thread: no memory of the first

	if len(w.got[0].History) != 0 {
		t.Errorf("the first question has no history: %+v", w.got[0].History)
	}
	h := w.got[1].History
	if len(h) != 2 || h[0] != (HistoryTurn{Role: "user", Text: "What stage is MedTech in?"}) || h[1].Role != "cliff" || !strings.Contains(h[1].Text, "Negotiation") {
		t.Errorf("the follow-up must carry the first exchange: %+v", h)
	}
	if w.got[1].Text != "and EcoLite?" {
		t.Errorf("the follow-up text must stay as typed: %q", w.got[1].Text)
	}
	if len(w.got[2].History) != 0 {
		t.Errorf("a different thread must not see it: %+v", w.got[2].History)
	}
}

func TestTurnsPastTheWindowAreSummarisedAndTheLastEightStayWordForWord(t *testing.T) {
	w := &hookWorker{fn: func(call int, _ WorkerRequest) (WorkerAnswer, error) {
		return plainAnswer(fmt.Sprintf("answer %d", call)), nil
	}}
	s := newService(t, w, medtech(), nil)
	for i := 1; i <= 7; i++ { // 14 stored turns
		ask(t, s, "D1:", fmt.Sprintf("question %d about the Zephyr account", i))
	}
	ask(t, s, "D1:", "and now?")
	h := w.got[7].History
	if len(h) != 1+VerbatimTurns {
		t.Fatalf("history = %d entries, want a summary and %d turns: %+v", len(h), VerbatimTurns, h)
	}
	if h[0].Role != "summary" || !strings.Contains(h[0].Text, "question 1 about the Zephyr account") || len([]rune(h[0].Text)) > summaryLimit {
		t.Errorf("summary = %q", h[0].Text)
	}
	if h[1].Text != "question 4 about the Zephyr account" || h[8].Text != "answer 7" {
		t.Errorf("the verbatim window is wrong: first %q last %q", h[1].Text, h[8].Text)
	}
}

func TestMemoryNeedsAConversationReference(t *testing.T) {
	w := &hookWorker{fn: func(int, WorkerRequest) (WorkerAnswer, error) { return plainAnswer("ok"), nil }}
	s := newService(t, w, medtech(), nil)
	ask(t, s, "", "one")
	ask(t, s, "", "two")
	if len(w.got[1].History) != 0 {
		t.Errorf("no thread_ref, no memory: %+v", w.got[1].History)
	}
}

// proposing step one of "play the next event and tell me what changed": the task pauses, a confirmed Run waits for the
// pipeline and resumes it with the question and the action's result.
func chained(t *testing.T) (*Service, *hookWorker, *fakeBackend) {
	t.Helper()
	b := medtech()
	w := &hookWorker{fn: func(call int, _ WorkerRequest) (WorkerAnswer, error) {
		if call == 1 {
			return WorkerAnswer{AnswerMarkdown: "I will play the next event first, then tell you what changed.",
				ProposedAction: &WorkerAction{Kind: "play_next", Summary: "whatever the model says"}, Model: "scripted"}, nil
		}
		return WorkerAnswer{AnswerMarkdown: "The buyer asked for a security review [t1].", Model: "scripted", ToolCalls: 1,
			Citations: []WorkerCitation{{CallID: "t1", Tool: "timeline", Label: "Timeline", URL: str1(webBase + "/accounts/" + acctID)}}}, nil
	}}
	s := newService(t, w, b, nil)
	clock := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	s.sleep = func(_ context.Context, d time.Duration) { clock = clock.Add(d) } // waiting moves the clock, not the test
	return s, w, b
}

func TestAChainedTaskPausesOnTheConfirmationAndResumesAfterTheActionRan(t *testing.T) {
	s, w, b := chained(t)
	first := ask(t, s, "C1:100", "Play the next event and tell me what changed")
	if first.ProposedAction == nil || first.ProposedAction.Kind != "play_next" || len(w.got) != 1 {
		t.Fatalf("step one must propose and stop: %+v", first)
	}
	if len(b.posts) != 0 {
		t.Fatalf("nothing may run before the confirmation: %v", b.posts)
	}
	b.routes["/replay/manifests/"+manifestID+"/progress"] = ok(map[string]any{"overall": "complete", "stages": []any{
		map[string]any{"stage": "evals", "status": "warning"}}})
	res, err := s.RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext, User: "U1", ThreadRef: "C1:100", TurnID: "turn-resume-1"})
	if err != nil || res.Status != "done" {
		t.Fatalf("%+v %v", res, err)
	}
	if len(b.posts) != 1 || res.Continuation == nil || !strings.Contains(res.Continuation.AnswerMarkdown, "security review") {
		t.Fatalf("the task must continue after the action: %+v", res)
	}
	if len(w.got) != 2 || !strings.Contains(w.got[1].Text, "Play the next event and tell me what changed") || !strings.Contains(w.got[1].Text, "Released event 13 of 13") {
		t.Errorf("the resumed request must carry the original task and the action result: %q", w.got[1].Text)
	}
	if h := w.got[1].History; len(h) < 2 || h[0].Text != "Play the next event and tell me what changed" {
		t.Errorf("the resumed request keeps the conversation: %+v", h)
	}
	if lines := strings.Join(mustProgress(t, s, "turn-resume-1").Lines, "|"); !strings.Contains(lines, "Waiting for the pipeline") || !strings.Contains(lines, "Evals: warning") {
		t.Errorf("the wait must show: %s", lines)
	}
	// a second press resumes nothing
	again, _ := s.RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext, User: "U1", ThreadRef: "C1:100"})
	if again.Continuation != nil || len(w.got) != 2 {
		t.Errorf("a repeated Run resumed the task twice: %+v", again)
	}
}

func TestOnlyTheActionThatWasProposedResumesTheTask(t *testing.T) {
	s, w, _ := chained(t)
	ask(t, s, "C1:100", "Play the next event and tell me what changed")
	res, _ := s.RunAction(context.Background(), ActionRequest{Kind: ActionDemoStatus, User: "U1", ThreadRef: "C1:100"})
	if res.Continuation != nil || len(w.got) != 1 {
		t.Errorf("a different action must not resume the task: %+v", res)
	}
	res, _ = s.RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext, User: "U1"}) // no thread
	if res.Continuation != nil || len(w.got) != 1 {
		t.Errorf("without a conversation there is nothing to resume: %+v", res)
	}
}

func TestAPausedTaskExpiresAndANewQuestionClearsIt(t *testing.T) {
	s, w, _ := chained(t)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	ask(t, s, "C1:100", "Play the next event and tell me what changed")
	now = now.Add(pendingTTL + time.Minute)
	res, _ := s.RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext, User: "U1", ThreadRef: "C1:100"})
	if res.Continuation != nil || len(w.got) != 1 {
		t.Errorf("an old paused task resumed: %+v", res)
	}
	now = time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)
	ask(t, s, "C1:101", "Play the next event and tell me what changed")
	w.fn = func(int, WorkerRequest) (WorkerAnswer, error) { return plainAnswer("Never mind."), nil }
	ask(t, s, "C1:101", "never mind, just tell me the stage")
	res, _ = s.RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext, User: "U1", ThreadRef: "C1:101"})
	if res.Continuation != nil {
		t.Errorf("a newer question must clear the paused task: %+v", res)
	}
}

func TestARefusedActionDoesNotResumeTheTask(t *testing.T) {
	s, w, b := chained(t)
	ask(t, s, "C1:100", "Play the next event and tell me what changed")
	b.routes["POST /replay/manifests/"+manifestID+"/episodes/next"] = reply{409, map[string]any{"error": map[string]any{"code": "replay_complete", "message": "x"}}}
	res, _ := s.RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext, User: "U1", ThreadRef: "C1:100"})
	if res.Status != "refused" || res.Continuation != nil || len(w.got) != 1 {
		t.Errorf("%+v", res)
	}
}

func TestIfTheResumedTaskFailsTheActionStillStandsAndTheReaderIsTold(t *testing.T) {
	s, w, _ := chained(t)
	ask(t, s, "C1:100", "Play the next event and tell me what changed")
	w.fn = func(int, WorkerRequest) (WorkerAnswer, error) {
		return WorkerAnswer{}, errors.New("workerclient: boom")
	}
	res, err := s.RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext, User: "U1", ThreadRef: "C1:100"})
	if err != nil || res.Status != "done" || res.Continuation == nil || res.Continuation.AnswerMarkdown != continuationFailed {
		t.Errorf("%+v %v", res, err)
	}
}

func mustProgress(t *testing.T, s *Service, id string) Progress {
	t.Helper()
	p, err := s.Progress(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProgressLinesAppearWhileTheWorkerWorksAndTheTraceKeepsEveryStep(t *testing.T) {
	var s *Service
	var during Progress
	w := &hookWorker{fn: func(_ int, req WorkerRequest) (WorkerAnswer, error) {
		if _, err := s.RunTool(context.Background(), req.AskToken, "account_state", map[string]any{"account": "MedTech", "as_of": "2023-11-09"}); err != nil {
			t.Error(err)
		}
		if _, err := s.RunTool(context.Background(), req.AskToken, "gate_results", map[string]any{"account": "MedTech", "gate": "D4"}); err != nil {
			t.Error(err)
		}
		during = mustProgress(t, s, "turn-progress-1")
		cost := 0.0123
		return WorkerAnswer{AnswerMarkdown: "Stage Negotiation [t1].", ToolCalls: 2, Model: "scripted/m",
			Citations: []WorkerCitation{{CallID: "t1", Tool: "account_state", Label: "State"}},
			Usage:     &WorkerUsage{InputTokens: 1200, OutputTokens: 300, CostUSD: &cost}}, nil
	}}
	s = newService(t, w, medtech(), nil)
	if p := mustProgress(t, s, "turn-progress-1"); p.State != ProgressUnknown || len(p.Lines) != 0 {
		t.Errorf("before the turn: %+v", p)
	}
	ans, err := s.Ask(context.Background(), Request{Text: "q", ChannelKind: ChannelDM, User: "U1", ThreadRef: "D1:", TurnID: "turn-progress-1"})
	if err != nil {
		t.Fatal(err)
	}
	if during.State != ProgressWorking || strings.Join(during.Lines, "|") != "Reading MedTech's state as of Nov 9…|Checking the D4 evals…" {
		t.Errorf("during = %+v", during)
	}
	if after := mustProgress(t, s, "turn-progress-1"); after.State != ProgressDone || len(after.Lines) != 2 {
		t.Errorf("after = %+v", after)
	}
	tr, err := s.Trace(context.Background(), ans.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Steps) != 2 || tr.Steps[0].Tool != "account_state" || tr.Steps[0].Args["account"] != "MedTech" || tr.Steps[0].Output == nil ||
		tr.Steps[1].N != 2 || tr.TokensIn != 1200 || tr.TokensOut != 300 || tr.CostUSD == nil || *tr.CostUSD != 0.0123 ||
		tr.Model != "scripted/m" || tr.Question != "q" || tr.ThreadRef != "D1:" || tr.AnswerMarkdown != ans.AnswerMarkdown {
		t.Errorf("trace = %+v", tr)
	}
	if ans.TraceURL == nil || *ans.TraceURL != webBase+"/ask/traces/"+ans.TraceID || !strings.HasSuffix(ans.AnswerMarkdown, "("+*ans.TraceURL+")") {
		t.Errorf("every answer ends with its trace link: %+v", ans)
	}
	if _, err := s.Trace(context.Background(), "tr_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if _, err := s.Progress("x"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a malformed turn id: %v", err)
	}
}

func TestALargeToolOutputIsBoundedInTheTrace(t *testing.T) {
	big := strings.Repeat("x", 3*maxStepOutput)
	out, _ := boundOutput(map[string]any{"text": big}).(map[string]any)
	if out["truncated"] != true || len(out["preview"].(string)) > maxStepOutput {
		t.Errorf("output = %v", out)
	}
	small := map[string]any{"a": 1}
	if got := boundOutput(small).(map[string]any); got["a"] != 1 {
		t.Errorf("a small output must pass unchanged: %v", got)
	}
}

func TestAnswersNeverSayKnowledgeInfluencedAnything(t *testing.T) {
	cases := map[string]string{
		"Knowledge influenced the recommendation. The buyer wants a review.":    "Retrieved, applicable and used are different things; none of them measures influence. The buyer wants a review.",
		"The retrieved playbook influenced option A.":                           influenceRewrite,
		"The knowledge that was used is influencing the draft!":                 influenceRewrite,
		"Retrieval is not influence, and none of it measures influence.":        "Retrieval is not influence, and none of it measures influence.",
		"Priya is an influencer at the buyer and influences the CFO.":           "Priya is an influencer at the buyer and influences the CFO.",
		"Knowledge influenced A. The guidance influenced B. Nothing else here.": influenceRewrite + " Nothing else here.",
	}
	for in, want := range cases {
		w := &hookWorker{fn: func(int, WorkerRequest) (WorkerAnswer, error) { return plainAnswer(in), nil }}
		got := ask(t, newService(t, w, medtech(), nil), "", "q").AnswerMarkdown
		if !strings.HasPrefix(got, want) {
			t.Errorf("%q\n got  %q\n want %q", in, got, want)
		}
		if strings.Contains(strings.ToLower(strings.ReplaceAll(got, influenceRewrite, "")), "knowledge influenced") {
			t.Errorf("an influence claim survived: %q", got)
		}
	}
}

func TestTheWorkerHasLongerThanTheLoopItRunsAndCoreHasLongerStill(t *testing.T) {
	if TokenTTL < 150*time.Second {
		t.Errorf("an ask token must outlive a 120 s loop and a resumed task: %v", TokenTTL)
	}
}
