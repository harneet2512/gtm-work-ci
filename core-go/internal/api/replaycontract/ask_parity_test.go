package replaycontract

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ask"
	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/demoboundary"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// teeExtractor sends every extraction through the real worker client, to a recording worker, and then lets the
// deterministic fake decide the claims, so the pipeline behaves as in every other test here while the requests the
// worker would receive are captured exactly as workerclient wires them.
type teeExtractor struct {
	t      *testing.T
	worker *workerclient.Client
	then   claims.Extractor
}

func (e teeExtractor) Extract(ctx context.Context, req claims.ExtractRequest) (claims.ExtractResponse, error) {
	if _, err := e.worker.Extract(ctx, req); err != nil {
		e.t.Errorf("the recording worker refused a request: %v", err)
	}
	return e.then.Extract(ctx, req)
}

var uuidIn = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// anonymous replaces each distinct id by the order it first appears, so two worlds seeded with different random ids
// compare equal when they send the same requests.
func anonymous(body string) string {
	seen := map[string]int{}
	return uuidIn.ReplaceAllStringFunc(body, func(id string) string {
		if _, ok := seen[id]; !ok {
			seen[id] = len(seen) + 1
		}
		return "<id" + string(rune('0'+seen[id])) + ">"
	})
}

// workerBodies runs a fresh replay world whose extractions go to a recording worker. It advances the two history
// episodes, then calls trigger for the third (the held-out event, the one Play releases), and returns the request
// bodies the worker received because of the trigger.
func workerBodies(t *testing.T, trigger func(s *server)) []string {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, anonymous(string(raw)))
		mu.Unlock()
		_, _ = io.WriteString(w, `{"claims":[],"model":"recording-worker","extractor_version":"extract-v1","dropped":0}`)
	}))
	defer worker.Close()
	client, err := workerclient.New(worker.URL)
	if err != nil {
		t.Fatal(err)
	}
	s := newServerWith(t, nil, teeExtractor{t: t, worker: client, then: replaytest.BlockerExtractor()})
	for k := 1; k <= 2; k++ {
		if r := s.next(s.manifest); r.status != 200 {
			t.Fatalf("history episode %d: %d %s", k, r.status, r.body)
		}
	}
	mu.Lock()
	before := len(bodies)
	mu.Unlock()
	trigger(s)
	mu.Lock()
	defer mu.Unlock()
	return append([]string{}, bodies[before:]...)
}

// TestWebPlayAndCliffPlayNextSendTheWorkerTheSameRequests runs the same event through the web Play route and through
// Cliff's play_next, each in a fresh world, and compares what the worker client sent. The web request is built from the
// route the boundary contract names for the visible trigger, not from a string of this test's own.
func TestWebPlayAndCliffPlayNextSendTheWorkerTheSameRequests(t *testing.T) {
	root, err := demoboundary.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	b, err := demoboundary.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	method, route, _ := strings.Cut(b.VisibleTrigger.CoreEndpoint, " ")
	if method != http.MethodPost || !strings.Contains(route, "{manifest_id}") {
		t.Fatalf("the visible trigger is %q", b.VisibleTrigger.CoreEndpoint)
	}

	viaWeb := workerBodies(t, func(s *server) {
		if r := s.do(method, strings.ReplaceAll(route, "{manifest_id}", s.manifest), route, apiToken, nil); r.status != 200 {
			t.Fatalf("web Play: %d %s", r.status, r.body)
		}
	})
	viaCliff := workerBodies(t, func(s *server) {
		signer, err := ask.NewSigner([]byte(strings.Repeat("k", 40)))
		if err != nil {
			t.Fatal(err)
		}
		loopback := ask.NewLoopback(apiToken)
		loopback.Bind(s.srv.Config.Handler) // the same router web Play posts to, in process
		svc := ask.New(nil, loopback, signer, ask.Config{ManifestID: s.manifest}, nil)
		res, err := svc.RunAction(context.Background(), ask.ActionRequest{Kind: ask.ActionPlayNext, User: "U1"})
		if err != nil || res.Status != "done" {
			t.Fatalf("Cliff play_next: %+v %v", res, err)
		}
	})

	if len(viaWeb) == 0 {
		t.Fatal("releasing the held-out event sent the worker nothing, so there is nothing to compare")
	}
	if len(viaWeb) != len(viaCliff) {
		t.Fatalf("web Play sent %d worker requests, Cliff %d", len(viaWeb), len(viaCliff))
	}
	for i := range viaWeb {
		if viaWeb[i] != viaCliff[i] {
			t.Errorf("worker request %d differs\n web   %s\n cliff %s", i+1, viaWeb[i], viaCliff[i])
		}
	}
}
