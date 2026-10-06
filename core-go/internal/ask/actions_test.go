package ask

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPlayNextPostsTheWebPlayRouteWithNoBody(t *testing.T) {
	b := medtech()
	res, err := newService(t, &fakeWorker{}, b, nil).RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext, User: "U1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.posts) != 1 || b.posts[0] != "/replay/manifests/"+manifestID+"/episodes/next" {
		t.Fatalf("posts = %v", b.posts)
	}
	if res.Status != "done" || !strings.Contains(res.MessageMarkdown, "Released event 13 of 13") {
		t.Errorf("result = %+v", res)
	}
}

// That web Play and Cliff play_next make the worker see the same requests is tested at the worker client in
// internal/api/replaycontract (TestWebPlayAndCliffPlayNextSendTheWorkerTheSameRequests).

func TestPlayNextConflictsAreRefusalsInProductWords(t *testing.T) {
	for code, want := range map[string]string{"replay_complete": "already released", "play_in_progress": "still running"} {
		b := medtech()
		b.routes["POST /replay/manifests/"+manifestID+"/episodes/next"] = reply{409, map[string]any{"error": map[string]any{"code": code, "message": "x"}}}
		res, err := newService(t, &fakeWorker{}, b, nil).RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext, User: "U1"})
		if err != nil || res.Status != "refused" || res.Reason != code || !strings.Contains(res.MessageMarkdown, want) {
			t.Errorf("%s: %+v %v", code, res, err)
		}
	}
	b := medtech()
	b.routes["POST /replay/manifests/"+manifestID+"/episodes/next"] = reply{500, map[string]any{}}
	if _, err := newService(t, &fakeWorker{}, b, nil).RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext, User: "U1"}); err == nil {
		t.Error("a 500 must be an error, not a success")
	}
}

func TestWithoutAManifestThereIsNothingToPlay(t *testing.T) {
	sg, _ := NewSigner(secret)
	s := New(&fakeWorker{}, medtech(), sg, Config{}, nil)
	for _, k := range []string{ActionPlayNext, ActionDemoStatus} {
		res, err := s.RunAction(context.Background(), ActionRequest{Kind: k, User: "U1"})
		if err != nil || res.Status != "refused" || res.Reason != "not_configured" {
			t.Errorf("%s: %+v %v", k, res, err)
		}
	}
}

func TestDemoStatusIsReadOnlyAndInProductWords(t *testing.T) {
	b := medtech()
	res, err := newService(t, &fakeWorker{}, b, nil).RunAction(context.Background(), ActionRequest{Kind: ActionDemoStatus, User: "U1"})
	if err != nil || res.Status != "done" {
		t.Fatal(res, err)
	}
	for _, want := range []string{"Replay status: running", "Event received: completed", "Evals: waiting"} {
		if !strings.Contains(res.MessageMarkdown, want) {
			t.Errorf("status lacks %q: %s", want, res.MessageMarkdown)
		}
	}
	if len(b.posts) != 0 {
		t.Error("status must not post")
	}
}

func TestUnknownActionsAreRejected(t *testing.T) {
	s := newService(t, &fakeWorker{}, medtech(), nil)
	for _, k := range []string{"send_email", "crm_write", ""} {
		if _, err := s.RunAction(context.Background(), ActionRequest{Kind: k, User: "U1"}); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%q: err = %v", k, err)
		}
	}
	if _, err := s.RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext}); !errors.Is(err, ErrBadRequest) {
		t.Error("an action with no user must be rejected")
	}
}
