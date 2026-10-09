package fakecore_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/api/openapitest"
	"github.com/harneet2512/gtm-work/core-go/internal/fakecore"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

const token = "fakecore-test-token-0123456789"

type harness struct {
	t       *testing.T
	srv     *httptest.Server
	core    *fakecore.Server
	spec    *openapitest.Spec
	logPath string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	spec, err := openapitest.Load()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "log", "fakecore-log.json")
	core, err := fakecore.New(fakecore.Options{Token: token, LogPath: logPath})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(core.Handler())
	t.Cleanup(srv.Close)
	return &harness{t: t, srv: srv, core: core, spec: spec, logPath: logPath}
}

// call sends a request and, when template names a spec operation, checks the answer against the contract.
func (h *harness) call(method, path, template, tok string, body any) (int, []byte) {
	h.t.Helper()
	var rdr io.Reader
	if s, isString := body.(string); isString {
		rdr = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rdr)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if template != "" {
		if err := h.spec.Response(method, template, resp.StatusCode, raw); err != nil {
			h.t.Errorf("%s %s -> %d violates the contract: %v\n%s", method, path, resp.StatusCode, err, raw)
		}
	}
	return resp.StatusCode, raw
}

func (h *harness) do(method, path, template string, body any) (int, map[string]any) {
	h.t.Helper()
	status, raw := h.call(method, path, template, token, body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return status, m
}

func (h *harness) want(status int, got int, what string) {
	h.t.Helper()
	if got != status {
		h.t.Fatalf("%s: status %d, want %d", what, got, status)
	}
}

const (
	run     = "/runs/" + slacksurface.FixtureRunID
	episode = "/episodes/" + slacksurface.FixtureEpisodeID
	candA   = "0ca00000-0000-4000-8000-0000000000a1"
	candB   = "0ca00000-0000-4000-8000-0000000000a2"
	candC   = "0ca00000-0000-4000-8000-0000000000a3"
)

func chooseBody(cand string, extra map[string]any) map[string]any {
	m := map[string]any{"selected_candidate_id": cand, "surface": "slack", "actor_label": "alex"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestTheDemoPathConformsToTheContractAndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	const (
		decision = "/runs/{run_id}/strategy-decision"
		send     = "/runs/{run_id}/send"
	)

	status, bi := h.do("GET", "/accounts/"+slacksurface.FixtureAccountID+"/business-intelligence/latest", "/accounts/{account_id}/business-intelligence/latest", nil)
	h.want(200, status, "bi")
	if bi["account_id"] != slacksurface.FixtureAccountID {
		t.Fatalf("bi = %v", bi)
	}
	for i := 0; i < 3; i++ { // View full and the chooser read the same, unchanged set
		status, _ = h.do("GET", run+"/strategies", "/runs/{run_id}/strategies", nil)
		h.want(200, status, "strategies")
	}
	if n := len(h.core.Log().StrategiesSHA256); n != 1 {
		t.Fatalf("%d distinct strategy bodies served, want 1 (no regeneration)", n)
	}
	status, _ = h.do("GET", run+"/strategy-decision", decision, nil)
	h.want(404, status, "decision before choice")
	status, _ = h.do("GET", episode+"/judgment-inference", "/episodes/{episode_id}/judgment-inference", nil)
	h.want(404, status, "inference before send")

	status, d := h.do("POST", run+"/strategy-decision", decision, chooseBody(candB, nil))
	h.want(201, status, "choose")
	if d["selected_candidate_id"] != candB || d["original_agent_preference"] != candA || d["send_decision"] != "pending" {
		t.Fatalf("decision = %v", d)
	}
	status, _ = h.do("POST", run+"/strategy-decision", decision, chooseBody(candB, nil))
	h.want(200, status, "choose again")
	status, locked := h.do("POST", run+"/strategy-decision", decision, chooseBody(candC, nil))
	h.want(409, status, "different candidate")
	if locked["error"].(map[string]any)["code"] != "selection_locked" {
		t.Fatalf("conflict = %v", locked)
	}

	subject := "Edited subject"
	edit := chooseBody(candB, map[string]any{"final_artifact": map[string]any{"channel": "email", "subject": subject, "body": "Edited body"}})
	status, d = h.do("POST", run+"/strategy-decision", decision, edit)
	h.want(200, status, "edit")
	if edits, _ := d["edits"].([]any); len(edits) == 0 || d["final_artifact"] == nil {
		t.Fatalf("edit did not persist: %v", d)
	}

	status, d = h.do("POST", run+"/send", send, map[string]any{"decision": "send", "surface": "slack", "actor_label": "alex"})
	h.want(200, status, "send")
	if d["send_decision"] != "send" || d["human_decision_id"] == nil {
		t.Fatalf("sent = %v", d)
	}
	status, again := h.do("POST", run+"/send", send, map[string]any{"decision": "send", "surface": "slack", "actor_label": "alex"})
	h.want(409, status, "second send")
	if again["send_decision"] != "send" {
		t.Fatalf("already_decided must carry the stored record: %v", again)
	}
	status, _ = h.do("POST", run+"/strategy-decision", decision, edit)
	h.want(409, status, "edit after send")

	status, inf := h.do("GET", episode+"/judgment-inference", "/episodes/{episode_id}/judgment-inference", nil)
	h.want(200, status, "inference")
	if inf["human_choice"] != candB || inf["agent_preference"] != candA || inf["agreement"] != "overrode" || inf["human_verdict"] != "pending" {
		t.Fatalf("inference = %v", inf)
	}
	verdict := map[string]any{"verdict": "corrected", "corrected_statement": "Because the reviewer is new.", "note": "n", "surface": "slack", "actor_label": "alex"}
	status, inf = h.do("POST", episode+"/judgment-verdict", "/episodes/{episode_id}/judgment-verdict", verdict)
	h.want(200, status, "verdict")
	if inf["human_verdict"] != "corrected" || inf["human_note"] != "n" {
		t.Fatalf("verdict = %v", inf)
	}

	log := h.core.Log()
	if len(log.Effects) != 1 || log.Effects[0].IdempotencyKey != slacksurface.FixtureRunID+":execute" || log.Effects[0].Subject != subject {
		t.Fatalf("effects = %+v, want exactly one recorded send of the edited email", log.Effects)
	}
}

func TestRequestsTheContractRefusesAreRejectedAndLogged(t *testing.T) {
	h := newHarness(t)
	status, _ := h.call("POST", run+"/strategy-decision", "", token, `{"selected_candidate_id":"x","bogus":1}`)
	h.want(400, status, "body that violates the contract")
	status, _ = h.call("POST", run+"/send", "", token, `not json`)
	h.want(400, status, "body that is not JSON")
	status, _ = h.call("POST", run+"/send", "", token, map[string]any{"decision": "send", "surface": "slack", "actor_label": "a"})
	h.want(409, status, "send before a choice")
	status, _ = h.call("GET", run+"/strategies", "", "wrong", nil)
	h.want(401, status, "wrong token")
	status, _ = h.call("GET", run+"/strategies", "", "", nil)
	h.want(401, status, "no token")
	status, _ = h.call("GET", "/runs/"+slacksurface.FixtureAccountID+"/strategies", "", token, nil)
	h.want(404, status, "unknown run")
	status, _ = h.call("POST", run+"/strategy-decision", "", token, chooseBody(candB, map[string]any{
		"final_to": []any{map[string]any{"person_id": slacksurface.FixtureAccountID, "role": "to"}}}))
	h.want(422, status, "recipient outside the account")

	raw, err := os.ReadFile(h.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), "wrong") {
		t.Fatal("the request log contains a token")
	}
	var log fakecore.Log
	if err := json.Unmarshal(raw, &log); err != nil || len(log.Entries) != 7 {
		t.Fatalf("log: %v entries=%d", err, len(log.Entries))
	}
	if log.Entries[0].Status != 400 || log.Entries[0].Code != "invalid_request" {
		t.Fatalf("first entry = %+v", log.Entries[0])
	}
}

func TestChoosingTheGhostPreferenceIsConfirmedNotContradicted(t *testing.T) {
	h := newHarness(t)
	h.do("POST", run+"/strategy-decision", "", chooseBody(candA, nil))
	h.do("POST", run+"/send", "", map[string]any{"decision": "send", "surface": "slack", "actor_label": "alex"})

	status, inf := h.do("GET", episode+"/judgment-inference", "/episodes/{episode_id}/judgment-inference", nil)

	h.want(200, status, "inference")
	if inf["agreement"] != "agreed" {
		t.Fatalf("inference = %v", inf)
	}
}

func TestDiscardRecordsNoEffect(t *testing.T) {
	h := newHarness(t)
	h.do("POST", run+"/strategy-decision", "", chooseBody(candC, nil))
	status, d := h.do("POST", run+"/send", "/runs/{run_id}/send", map[string]any{"decision": "discard", "surface": "slack", "actor_label": "alex"})
	h.want(200, status, "discard")
	if d["send_decision"] != "discard" || len(h.core.Log().Effects) != 0 {
		t.Fatalf("decision=%v effects=%v", d, h.core.Log().Effects)
	}
}

func TestNewRefusesAnEmptyToken(t *testing.T) {
	if _, err := fakecore.New(fakecore.Options{}); err == nil {
		t.Fatal("a fake core without a token was built")
	}
}

// The Slack directory reads the account state beside the graph (slacksurface.CoreHTTP.People): the fake serves it, as the contract says.
func TestTheAccountStateIsServedAndConformsToTheContract(t *testing.T) {
	h := newHarness(t)
	status, st := h.do("GET", "/accounts/"+slacksurface.FixtureAccountID+"/state", "/accounts/{account_id}/state", nil)
	h.want(200, status, "account state")
	if st["account_id"] != slacksurface.FixtureAccountID {
		t.Fatalf("state is addressed to %v", st["account_id"])
	}
	status, _ = h.do("GET", "/accounts/00000000-0000-4000-8000-00000000dead/state", "", nil)
	h.want(404, status, "unknown account")
}
