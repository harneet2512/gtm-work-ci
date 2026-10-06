package contracttest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// HAR-136: the create-only message refs, GET, reserve and record-ts, checked against contracts/openapi/core.yaml.
func TestSurfaceMessageRefsConformToTheContract(t *testing.T) {
	s := newStack(t)
	seeded := strategytest.Seed(t, env.DB, s.world.AccountA)
	base := "/surface-messages/" + seeded.EpisodeID + "/slack/chooser"
	const (
		getT     = "/surface-messages/{subject_id}/{surface}/{kind}"
		reserveT = "/surface-messages/{subject_id}/{surface}/{kind}/reservation"
		tsT      = "/surface-messages/{subject_id}/{surface}/{kind}/ts"
	)
	type reservation struct {
		Created bool `json:"created"`
		Message struct {
			Channel string  `json:"channel"`
			TS      *string `json:"ts"`
		} `json:"message"`
	}

	if r := s.get(base, getT); r.status != 404 {
		t.Fatalf("before any reservation: %d %s", r.status, clip(r.body))
	}
	if r := s.do("PUT", base+"/ts", tsT, apiToken, []byte(`{"ts":"1759587744.000200"}`)); r.status != 404 {
		t.Fatalf("record before reserve: %d %s", r.status, clip(r.body))
	}

	var res reservation
	r := s.do("POST", base+"/reservation", reserveT, apiToken, []byte(`{"channel":"C1"}`))
	if r.status != 200 || json.Unmarshal(r.body, &res) != nil || !res.Created || res.Message.TS != nil || res.Message.Channel != "C1" {
		t.Fatalf("reserve: %d %s", r.status, clip(r.body))
	}
	r = s.do("POST", base+"/reservation", reserveT, apiToken, []byte(`{"channel":"C2"}`))
	if r.status != 200 || json.Unmarshal(r.body, &res) != nil || res.Created || res.Message.Channel != "C1" {
		t.Fatalf("second reserve: %d %s", r.status, clip(r.body))
	}

	for i := 0; i < 2; i++ { // twice: the same ts is a no-op
		r = s.do("PUT", base+"/ts", tsT, apiToken, []byte(`{"ts":"1759587744.000200"}`))
		if r.status != 200 || !strings.Contains(string(r.body), `"ts":"1759587744.000200"`) {
			t.Fatalf("record %d: %d %s", i, r.status, clip(r.body))
		}
	}
	r = s.do("PUT", base+"/ts", tsT, apiToken, []byte(`{"ts":"1759587744.000999"}`))
	var conflict struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if r.status != 409 || json.Unmarshal(r.body, &conflict) != nil || conflict.Error.Code != "ts_conflict" || conflict.Error.Details["ts"] != "1759587744.000200" {
		t.Fatalf("a different ts: %d %s, want 409 ts_conflict carrying the recorded ts", r.status, clip(r.body))
	}
	if r := s.get(base, getT); r.status != 200 || !strings.Contains(string(r.body), `"ts":"1759587744.000200"`) {
		t.Fatalf("get: %d %s", r.status, clip(r.body))
	}

	for name, c := range map[string]struct {
		method, path, template, body string
	}{
		"bad subject":     {"GET", "/surface-messages/nope/slack/chooser", getT, ""},
		"bad surface":     {"GET", "/surface-messages/" + seeded.EpisodeID + "/Slack/chooser", getT, ""},
		"bad kind":        {"GET", "/surface-messages/" + seeded.EpisodeID + "/slack/message2", getT, ""},
		"reserve no body": {"POST", base + "/reservation", reserveT, `{}`},
		"reserve extra":   {"POST", base + "/reservation", reserveT, `{"channel":"C1","x":1}`},
		"reserve channel": {"POST", base + "/reservation", reserveT, `{"channel":""}`},
		"ts not a ts":     {"PUT", base + "/ts", tsT, `{"ts":"yesterday"}`},
		"ts no body":      {"PUT", base + "/ts", tsT, `{`},
	} {
		if r := s.do(c.method, c.path, c.template, apiToken, []byte(c.body)); r.status != 400 {
			t.Errorf("%s: %d %s, want 400", name, r.status, clip(r.body))
		}
	}
	for name, c := range map[string]struct{ method, path, template string }{
		"get":     {"GET", base, getT},
		"reserve": {"POST", base + "/reservation", reserveT},
		"ts":      {"PUT", base + "/ts", tsT},
	} {
		if r := s.do(c.method, c.path, c.template, "", []byte(`{}`)); r.status != 401 {
			t.Errorf("%s without a token: %d", name, r.status)
		}
	}
	for name, c := range map[string]struct{ method, path string }{
		"delete the ref":    {"DELETE", base},
		"get a reserve":     {"GET", base + "/reservation"},
		"post the ts":       {"POST", base + "/ts"},
		"put a reservation": {"PUT", base + "/reservation"},
	} {
		if r := s.do(c.method, c.path, "", apiToken, nil); r.status != 405 {
			t.Errorf("%s: %d, want 405", name, r.status)
		}
	}
	if strings.Contains(s.logs.String(), "surface message") {
		t.Fatalf("unexpected server errors: %s", s.logs.String())
	}
}
