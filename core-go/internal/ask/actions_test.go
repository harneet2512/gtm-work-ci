package ask

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

// Both triggers reach the same handler with the same request, so the worker sees the same calls and the recorded
// answers replay the same way: the handler cannot tell a Cliff play_next from the web Play button.
func TestPlayNextAndWebPlayProduceTheSameRequestAtTheHandler(t *testing.T) {
	type seen struct{ method, path, auth, body string }
	var log []seen
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 16)
		n, _ := r.Body.Read(buf)
		log = append(log, seen{r.Method, r.URL.Path, r.Header.Get("Authorization"), string(buf[:n])})
		_, _ = w.Write([]byte(`{"episode":2,"total":3}`))
	})
	lb := NewLoopback("op")
	lb.Bind(h)
	if _, err := newService(t, &fakeWorker{}, lb, nil).RunAction(context.Background(), ActionRequest{Kind: ActionPlayNext, User: "U1"}); err != nil {
		t.Fatal(err)
	}
	web := httptest.NewRequest("POST", "/replay/manifests/"+manifestID+"/episodes/next", http.NoBody) // what web/lib/api/core-client.ts sends
	web.Header.Set("Authorization", "Bearer op")
	h.ServeHTTP(httptest.NewRecorder(), web)
	if len(log) != 2 || log[0] != log[1] {
		t.Fatalf("Cliff %+v, web %+v", log[0], log[len(log)-1])
	}
}

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
