package ask

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demoboundary"
)

type fakeWorker struct {
	got    []WorkerRequest
	answer WorkerAnswer
	err    error
}

func (f *fakeWorker) Ask(_ context.Context, r WorkerRequest) (WorkerAnswer, error) {
	f.got = append(f.got, r)
	return f.answer, f.err
}

var secret = []byte(strings.Repeat("s", 40))

func newService(t *testing.T, w Worker, b Backend, _ any) *Service {
	t.Helper()
	sg, err := NewSigner(secret)
	if err != nil {
		t.Fatal(err)
	}
	s := New(w, b, sg, Config{WebBase: webBase, ManifestID: manifestID}, nil)
	clock := time.Now()
	s.now = func() time.Time { return clock }
	s.sleep = func(_ context.Context, d time.Duration) { clock = clock.Add(d) } // waiting moves the clock, not the test
	return s
}

func str1(s string) *string { return &s }

func TestAskForwardsOnlyTheQuestionAndATokenTheToolsAccept(t *testing.T) {
	w := &fakeWorker{answer: WorkerAnswer{AnswerMarkdown: "MedTech wants a security review [t1].", ToolCalls: 1,
		Citations: []WorkerCitation{{CallID: "t1", Tool: "account_state", Label: "MedTech in gtm_ai", URL: str1(webBase + "/accounts/" + acctID)}}}}
	s := newService(t, w, medtech(), nil)
	ans, err := s.Ask(context.Background(), Request{Text: "  What changed at MedTech on Nov 9? ", ChannelKind: ChannelDM, User: "U1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.got) != 1 || w.got[0].Text != "What changed at MedTech on Nov 9?" || w.got[0].ChannelKind != "dm" {
		t.Fatalf("worker request = %+v", w.got)
	}
	if _, err := s.RunTool(context.Background(), w.got[0].AskToken, "account_state", map[string]any{"account": "MedTech"}); err != nil {
		t.Errorf("the minted token must open the tools: %v", err)
	}
	want := "MedTech wants a security review.\n\nOpen in gtm_ai: [MedTech in gtm_ai](" + webBase + "/accounts/" + acctID + ")"
	want += "\n\nTrace: [how I got this](" + webBase + "/ask/traces/" + ans.TraceID + ")"
	if ans.AnswerMarkdown != want || ans.TraceID == "" || ans.TraceURL == nil || len(ans.Citations) != 1 || ans.ProposedAction != nil {
		t.Errorf("answer = %+v", ans)
	}
}

func TestACitationLinkOutsideTheControlPlaneIsDropped(t *testing.T) {
	w := &fakeWorker{answer: WorkerAnswer{AnswerMarkdown: "x [t1]", Citations: []WorkerCitation{
		{CallID: "t1", Tool: "timeline", Label: "evil", URL: str1("http://evil.test/phish")},
		{CallID: "t2", Tool: "timeline", Label: "prefix trick", URL: str1("http://web.test.evil.test/x")}}}}
	ans, err := newService(t, w, medtech(), nil).Ask(context.Background(), Request{Text: "q", ChannelKind: ChannelThread, User: "U1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ans.Citations {
		if c.URL != nil {
			t.Errorf("a foreign link survived: %v", *c.URL)
		}
	}
	if strings.Contains(ans.AnswerMarkdown, "evil") && strings.Contains(ans.AnswerMarkdown, "http") {
		t.Errorf("answer carries a foreign link: %s", ans.AnswerMarkdown)
	}
}

func TestIDontKnowPassesThroughWithNoLinks(t *testing.T) {
	w := &fakeWorker{answer: WorkerAnswer{AnswerMarkdown: "I don't know. The data I can read does not show that.", DontKnow: true}}
	ans, _ := newService(t, w, medtech(), nil).Ask(context.Background(), Request{Text: "q", ChannelKind: ChannelDM, User: "U1"})
	if !strings.HasPrefix(ans.AnswerMarkdown, "I don't know") || strings.Contains(ans.AnswerMarkdown, "Open in") || len(ans.Citations) != 0 {
		t.Errorf("answer = %+v", ans)
	}
}

func TestDryRunAnswersAreLabelledDraftAndPreview(t *testing.T) {
	w := &fakeWorker{answer: WorkerAnswer{AnswerMarkdown: "Hi Dana, attached is the package.", Citations: []WorkerCitation{{CallID: "t1", Tool: "draft_followup", Label: "x"}}}}
	ans, _ := newService(t, w, medtech(), nil).Ask(context.Background(), Request{Text: "draft", ChannelKind: ChannelDM, User: "U1"})
	if !strings.HasPrefix(ans.AnswerMarkdown, "**DRAFT (dry run, nothing was sent)**") {
		t.Errorf("answer = %q", ans.AnswerMarkdown)
	}
	w.answer.Citations[0].Tool = "crm_update_preview"
	ans, _ = newService(t, w, medtech(), nil).Ask(context.Background(), Request{Text: "crm", ChannelKind: ChannelDM, User: "U1"})
	if !strings.HasPrefix(ans.AnswerMarkdown, "**PREVIEW (nothing was written to the CRM)**") {
		t.Errorf("answer = %q", ans.AnswerMarkdown)
	}
}

func TestAProposedActionAlwaysRequiresConfirmation(t *testing.T) {
	w := &fakeWorker{answer: WorkerAnswer{AnswerMarkdown: "I can play it.", ProposedAction: &WorkerAction{Kind: "play_next", Summary: "Release the next event"}}}
	ans, _ := newService(t, w, medtech(), nil).Ask(context.Background(), Request{Text: "Play the next event", ChannelKind: ChannelDM, User: "U1"})
	a := ans.ProposedAction
	if a == nil || a.Kind != ActionPlayNext || !a.RequiresConfirmation {
		t.Errorf("proposal = %+v", a)
	}
}

func TestAnInventedActionKindIsIgnored(t *testing.T) {
	w := &fakeWorker{answer: WorkerAnswer{AnswerMarkdown: "ok", ProposedAction: &WorkerAction{Kind: "send_email", Summary: "x"}}}
	ans, _ := newService(t, w, medtech(), nil).Ask(context.Background(), Request{Text: "q", ChannelKind: ChannelDM, User: "U1"})
	if ans.ProposedAction != nil {
		t.Errorf("proposal = %+v", ans.ProposedAction)
	}
}

func TestBadQuestionsNeverReachTheWorker(t *testing.T) {
	w := &fakeWorker{}
	s := newService(t, w, medtech(), nil)
	for name, r := range map[string]Request{
		"empty":    {Text: "  ", ChannelKind: ChannelDM, User: "U1"},
		"too long": {Text: strings.Repeat("x", MaxTextChars+1), ChannelKind: ChannelDM, User: "U1"},
		"channel":  {Text: "q", ChannelKind: "channel", User: "U1"},
		"no user":  {Text: "q", ChannelKind: ChannelDM},
	} {
		if _, err := s.Ask(context.Background(), r); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if len(w.got) != 0 {
		t.Error("the worker was called")
	}
}

func TestNoWorkerIsUnavailableAndAWorkerErrorPropagates(t *testing.T) {
	if _, err := newService(t, nil, medtech(), nil).Ask(context.Background(), Request{Text: "q", ChannelKind: ChannelDM, User: "U1"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v", err)
	}
	boom := errors.New("worker down")
	if _, err := newService(t, &fakeWorker{err: boom}, medtech(), nil).Ask(context.Background(), Request{Text: "q", ChannelKind: ChannelDM, User: "U1"}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestAnswersAreClippedToTheContractLength(t *testing.T) {
	w := &fakeWorker{answer: WorkerAnswer{AnswerMarkdown: strings.Repeat("é", MaxAnswerChars+50)}}
	ans, _ := newService(t, w, medtech(), nil).Ask(context.Background(), Request{Text: "q", ChannelKind: ChannelDM, User: "U1"})
	if len([]rune(ans.AnswerMarkdown)) > MaxAnswerChars {
		t.Errorf("answer has %d runes", len([]rune(ans.AnswerMarkdown)))
	}
}

func TestTokenIsRefusedWhenForgedExpiredOrNotAnAskToken(t *testing.T) {
	sg, _ := NewSigner(secret)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tok, _ := sg.Issue(now)
	if err := sg.Verify(tok, now.Add(time.Minute)); err != nil {
		t.Errorf("valid token refused: %v", err)
	}
	if err := sg.Verify(tok, now.Add(TokenTTL+time.Second)); !errors.Is(err, ErrBadToken) {
		t.Errorf("expired token accepted: %v", err)
	}
	other, _ := NewSigner([]byte(strings.Repeat("o", 40)))
	if err := other.Verify(tok, now); !errors.Is(err, ErrBadToken) {
		t.Error("a token signed with another key was accepted")
	}
	for _, bad := range []string{"", "at1.1.2.3", "rt1.0a000000-0000-4000-8000-000000000001.9999999999." + strings.Repeat("a", 64), strings.Repeat("x", 300)} {
		if err := sg.Verify(bad, now); !errors.Is(err, ErrBadToken) {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := NewSigner([]byte("short")); err == nil {
		t.Error("a short secret was accepted")
	}
}

func TestRunToolRefusesAMissingOrForeignToken(t *testing.T) {
	s := newService(t, &fakeWorker{}, medtech(), nil)
	if _, err := s.RunTool(context.Background(), "", "list_accounts", nil); !errors.Is(err, ErrBadToken) {
		t.Errorf("err = %v", err)
	}
}

func TestLinksOnlyIntoAnHTTPBase(t *testing.T) {
	if NewLinks("javascript:alert(1)").Base() != "" || NewLinks("").Base() != "" || NewLinks("/relative").Base() != "" {
		t.Error("a non-http base was accepted")
	}
	l := NewLinks(" http://web.test/ ")
	if l.Account("") != "" || l.Episode("e") != "http://web.test/episodes/e" || !l.Owns("http://web.test/x") || l.Owns("http://web.test.evil/x") {
		t.Error("link builders")
	}
	if NewLinks("").Account("a") != "" || NewLinks("").Evals("r") != "" || NewLinks("").Knowledge() != "" || NewLinks("").Replay("m") != "" || NewLinks("").Span("e", "s") != "" {
		t.Error("no base means no links")
	}
}

func TestLoopbackCallsTheRouterAsTheOperatorAndIsUnavailableUnbound(t *testing.T) {
	lb := NewLoopback("op-token")
	if _, _, err := lb.Get(context.Background(), "/x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v", err)
	}
	var got *http.Request
	lb.Bind(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	status, body, err := lb.Post(context.Background(), "/replay/manifests/m/episodes/next")
	if err != nil || status != 201 || string(body) != `{"ok":true}` {
		t.Fatalf("status=%d body=%s err=%v", status, body, err)
	}
	if got.Method != "POST" || got.URL.Path != "/replay/manifests/m/episodes/next" || got.Header.Get("Authorization") != "Bearer op-token" {
		t.Errorf("request = %s %s %v", got.Method, got.URL.Path, got.Header)
	}
}

func TestPlayNextCallsTheRouteTheBoundaryContractNames(t *testing.T) {
	root, err := demoboundary.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	b, err := demoboundary.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	web := b.VisibleTrigger.CoreEndpoint
	cliff := b.VisibleTrigger.Also[0].CoreEndpoint
	if web != cliff || web != "POST "+strings.Replace(PlayNextPath, "%s", "{manifest_id}", 1) {
		t.Fatalf("web Play %q, Cliff %q, ask route %q", web, cliff, PlayNextPath)
	}
}
