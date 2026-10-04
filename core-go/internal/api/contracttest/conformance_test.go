package contracttest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph/neo4jtest"
)

// pending are the core.yaml operations no work package has implemented yet. Adding an operation to
// the spec without a handler test or an entry here fails TestEveryOperationIsTestedOrPending.
var pending = map[string]string{
	"GET /accounts":                                         "WP9 HAR-107 (account list)",
	"GET /accounts/{account_id}":                            "WP9 HAR-107",
	"GET /accounts/{account_id}/claims":                     "WP9 HAR-107",
	"POST /accounts/{account_id}/claims/{claim_id}/confirm": "WP9 HAR-107 (needs HAR-106)",
	"GET /accounts/{account_id}/transitions":                "HAR-126 (transitionstore.List and History exist; the handler is not served yet)",
	"GET /runs":                                             "WP9 HAR-107 (needs HAR-106)",
	"POST /runs/{run_id}/decisions":                         "WP9 HAR-107 (needs HAR-106)",
	// HAR-129 demo objects (ADR-0017). The strategy, decision, verdict and business-intelligence endpoints are
	// implemented (strategy_test.go). Play and the invisibility check (HAR-124) are served by core and checked
	// against the spec in internal/api/replaycontract: the replay tests reset every table, so they run in a
	// package with its own database instead of this package's shared sample world.
	"POST /replay/play": "HAR-124, conformance-tested in internal/api/replaycontract",
	"GET /replay/manifests/{manifest_id}/invisibility":   "HAR-124, conformance-tested in internal/api/replaycontract",
	"GET /replay/manifests/{manifest_id}/episodes":       "HAR-129, conformance-tested in internal/api/replaycontract",
	"POST /replay/manifests/{manifest_id}/episodes/next": "HAR-129, conformance-tested in internal/api/replaycontract",
	"POST /replay/manifests/{manifest_id}/reset":         "HAR-129, conformance-tested in internal/api/replaycontract",
}

func TestReadEndpointsConformToTheContract(t *testing.T) {
	s := newStack(t)
	a := s.world.AccountA
	state := "/accounts/{account_id}/state"

	r := s.get("/accounts/"+a+"/state", state)
	if r.status != 200 {
		t.Fatalf("state: %d %s", r.status, clip(r.body))
	}
	future := url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if r := s.get("/accounts/"+a+"/state?as_of="+future, state); r.status != 200 {
		t.Fatalf("as_of state: %d %s", r.status, clip(r.body))
	}
	if r := s.get("/accounts/"+a+"/state?as_of=2000-01-01T00:00:00Z", state); r.status != 404 || errorCode(t, r) != "state_not_computed" {
		t.Fatalf("early as_of: %d %s", r.status, clip(r.body))
	}
	if r := s.get("/accounts/"+a+"/state?as_of=yesterday", state); r.status != 400 {
		t.Fatalf("bad as_of: %d", r.status)
	}
	if r := s.get("/accounts/"+missingID+"/state", state); r.status != 404 || errorCode(t, r) != "not_found" {
		t.Fatalf("unknown account: %d", r.status)
	}
	if r := s.get("/accounts/not-a-uuid/state", state); r.status != 404 {
		t.Fatalf("malformed account id: %d", r.status)
	}
	if r := s.do("GET", "/accounts/"+a+"/state", state, "", nil); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}

	for _, e := range []string{"diffs", "signals", "timeline"} {
		tmpl := "/accounts/{account_id}/" + e
		if r := s.get("/accounts/"+a+"/"+e, tmpl); r.status != 200 {
			t.Fatalf("%s: %d %s", e, r.status, clip(r.body))
		}
		if r := s.get("/accounts/"+a+"/"+e+"?limit=2", tmpl); r.status != 200 {
			t.Fatalf("%s limit=2: %d", e, r.status)
		}
		for _, bad := range []string{"limit=0", "limit=201", "limit=x"} {
			if r := s.get("/accounts/"+a+"/"+e+"?"+bad, tmpl); r.status != 400 {
				t.Fatalf("%s %s: %d", e, bad, r.status)
			}
		}
		if r := s.get("/accounts/"+missingID+"/"+e, tmpl); r.status != 404 {
			t.Fatalf("%s unknown account: %d", e, r.status)
		}
		if r := s.do("GET", "/accounts/"+a+"/"+e, tmpl, "wrong-token", nil); r.status != 401 {
			t.Fatalf("%s wrong token: %d", e, r.status)
		}
	}
	diffs := "/accounts/{account_id}/diffs"
	if r := s.get("/accounts/"+a+"/diffs?material_only=false", diffs); r.status != 200 {
		t.Fatalf("material_only=false: %d", r.status)
	}
	if r := s.get("/accounts/"+a+"/diffs?material_only=maybe", diffs); r.status != 400 {
		t.Fatalf("material_only=maybe: %d", r.status)
	}
	timeline := "/accounts/{account_id}/timeline"
	if r := s.get("/accounts/"+a+"/timeline?before="+future+"&limit=3", timeline); r.status != 200 {
		t.Fatalf("before: %d", r.status)
	}
	if r := s.get("/accounts/"+a+"/timeline?before=soon", timeline); r.status != 400 {
		t.Fatalf("bad before: %d", r.status)
	}
}

func TestPopulatedDiffsSignalsAndTraceConformToTheContract(t *testing.T) {
	s := newStack(t)
	a := s.world.AccountB
	run := ctxfixture.FreshRun(t, env.DB, a, "awaiting_human")
	diffID := seedLineage(t, a, run)

	diffs := s.get("/accounts/"+a+"/diffs", "/accounts/{account_id}/diffs")
	signals := s.get("/accounts/"+a+"/signals", "/accounts/{account_id}/signals")
	if diffs.status != 200 || !strings.Contains(string(diffs.body), diffID) || signals.status != 200 || !strings.Contains(string(signals.body), "stage_advanced") {
		t.Fatalf("diffs %d signals %d\n%s\n%s", diffs.status, signals.status, clip(diffs.body), clip(signals.body))
	}

	trace := "/runs/{run_id}/trace"
	r := s.get("/runs/"+run+"/trace", trace)
	if r.status != 200 || !strings.Contains(string(r.body), diffID) {
		t.Fatalf("trace: %d %s", r.status, clip(r.body))
	}
	if r := s.get("/runs/"+missingID+"/trace", trace); r.status != 404 {
		t.Fatalf("unknown run: %d", r.status)
	}
	if r := s.get("/runs/not-a-uuid/trace", trace); r.status != 404 {
		t.Fatalf("malformed run id: %d", r.status)
	}
	if r := s.do("GET", "/runs/"+run+"/trace", trace, "", nil); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
}

// seedLineage writes what HAR-106 will write for a run: a diff, a signal, the evaluation links, a
// step, a log row and a decision.
func seedLineage(t *testing.T, account, run string) (diffID string) {
	t.Helper()
	version := scalar(t, `SELECT version::text FROM account_state WHERE account_id = $1::uuid`, account)
	if _, err := env.DB.Exec(`INSERT INTO state_history (account_id, version, as_of, state)
 SELECT account_id, 5000, as_of, state FROM account_state WHERE account_id = $1::uuid ON CONFLICT DO NOTHING`, account); err != nil {
		t.Fatal(err)
	}
	// one diff per run: a trigger evaluation is unique per diff and workflow (migration 0016)
	next := scalar(t, `SELECT (5000 + count(*))::text FROM state_diffs WHERE account_id = $1::uuid AND to_version >= 5000`, account)
	if _, err := env.DB.Exec(`INSERT INTO state_history (account_id, version, as_of, state)
 SELECT account_id, $2::int, as_of, state FROM account_state WHERE account_id = $1::uuid ON CONFLICT DO NOTHING`, account, next); err != nil {
		t.Fatal(err)
	}
	diffID = scalar(t, `INSERT INTO state_diffs (account_id, from_version, to_version, is_material, changes)
 VALUES ($1::uuid, $2::int, $3::int, true, '[{"field":"stage","op":"changed","before":"a","after":"b","material":true}]'::jsonb)
 ON CONFLICT (account_id, to_version) DO UPDATE SET is_material = true RETURNING id::text`, account, version, next)
	signal := scalar(t, `INSERT INTO signals (account_id, signal_type, state_diff_id, rule, evidence_refs)
 VALUES ($1::uuid, 'stage_advanced', $2::uuid, 'sig.stage@1', '[]') RETURNING id::text`, account, diffID)
	for _, q := range []string{
		`UPDATE trigger_evaluations SET state_diff_id = '` + diffID + `', signal_ids = ARRAY['` + signal + `'::uuid]
 WHERE id = (SELECT trigger_evaluation_id FROM agent_runs WHERE id = '` + run + `')`,
		`INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status) VALUES ('` + run + `', 1, 'build_context', 'dry_run', 'succeeded')`,
		`INSERT INTO context_access_log (agent_run_id, tool, returned_ids, bytes) VALUES ('` + run + `', 'state', '[]', 2)`,
		`INSERT INTO human_decisions (agent_run_id, decision, surface, actor_label) VALUES ('` + run + `', 'approve', 'web', 'rep')`,
	} {
		if _, err := env.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return diffID
}

func TestContextPullsConformToTheContract(t *testing.T) {
	s := newStack(t)
	_, token := s.token(s.world.AccountA)
	for _, tool := range []string{"state", "recent_diffs", "activities", "people", "commitments"} {
		r := s.pull(token, tool, "")
		if r.status != 200 {
			t.Fatalf("%s: %d %s", tool, r.status, clip(r.body))
		}
		if p := decodePacket(t, r); string(p.Tool) != tool || p.AccessID < 1 {
			t.Fatalf("%s packet = %+v", tool, p)
		}
	}
	if r := s.pull(token, "evidence", "field_path=stage&limit=5"); r.status != 200 {
		t.Fatalf("evidence: %d %s", r.status, clip(r.body))
	}
	if r := s.pull(token, "state", "field_path=champion&limit=1"); r.status != 200 {
		t.Fatalf("state field: %d", r.status)
	}

	if r := s.pull(token, "evidence", ""); r.status != 400 {
		t.Fatalf("evidence without field_path: %d", r.status)
	}
	if r := s.pull(token, "nonsense", ""); r.status != 404 {
		t.Fatalf("unknown tool: %d", r.status)
	}
	if r := s.pull("", "state", ""); r.status != 401 || r.header.Get("WWW-Authenticate") == "" {
		t.Fatalf("no token: %d %v", r.status, r.header)
	}
	runID, tok := s.token(s.world.AccountB)
	if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'awaiting_human' WHERE id = $1::uuid`, runID); err != nil {
		t.Fatal(err)
	}
	if r := s.pull(tok, "state", ""); r.status != 403 || errorCode(t, r) != "forbidden" {
		t.Fatalf("run past drafted: %d %s", r.status, clip(r.body))
	}
	budget, btok := s.token(s.world.AccountB)
	if _, err := env.DB.Exec(`INSERT INTO context_access_log (agent_run_id, tool, bytes) SELECT $1::uuid, 'state', 2 FROM generate_series(1, $2)`, budget, corectx.MaxPulls); err != nil {
		t.Fatal(err)
	}
	if r := s.pull(btok, "state", ""); r.status != 429 {
		t.Fatalf("over budget: %d", r.status)
	}
}

func TestHealthAndIngestConformToTheContract(t *testing.T) {
	s := newStack(t)
	if r := s.do("GET", "/healthz", "/healthz", "", nil); r.status != 200 {
		t.Fatalf("healthz: %d", r.status)
	}
	fixture, err := os.ReadFile("../../../../fixtures/live/acme_marco_soc2_email.json")
	if err != nil {
		t.Fatal(err)
	}
	var unique map[string]any // a fresh object id per run, so the first delivery is always new
	if err := json.Unmarshal(fixture, &unique); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("<contracttest-%d@example.test>", time.Now().UnixNano())
	unique["source_object_id"] = id
	unique["payload"].(map[string]any)["message_id"] = id // an email's object id is its Message-ID
	raw, _ := json.Marshal(unique)
	if r := s.do("POST", "/ingest", "/ingest", apiToken, raw); r.status != 201 {
		t.Fatalf("ingest: %d %s", r.status, clip(r.body))
	}
	if r := s.do("POST", "/ingest", "/ingest", apiToken, raw); r.status != 200 {
		t.Fatalf("duplicate ingest: %d %s", r.status, clip(r.body))
	}
	if r := s.do("POST", "/ingest", "/ingest", apiToken, []byte(`{"nope":1}`)); r.status != 400 {
		t.Fatalf("bad ingest: %d", r.status)
	}
	if r := s.do("POST", "/ingest", "/ingest", "", raw); r.status != 401 {
		t.Fatalf("unauthenticated ingest: %d", r.status)
	}
	var event map[string]any
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	event["source_system"] = "nonsense"
	bad, _ := json.Marshal(event)
	if r := s.do("POST", "/ingest", "/ingest", apiToken, bad); r.status != 422 {
		t.Fatalf("invalid source system: %d %s", r.status, clip(r.body))
	}
}

// TestEveryOperationIsTestedOrPending closes the loop: each operation in core.yaml is either
// exercised by the conformance tests above or explicitly listed as not yet implemented, and an
// operation declared pending must really be absent (404), so the list cannot rot.
func TestEveryOperationIsTestedOrPending(t *testing.T) {
	s := newStack(t)
	// Exercise the endpoints once so s.checked is complete even when this test runs alone.
	t.Run("exercise", func(t *testing.T) {
		TestReadEndpointsConformToTheContract(t)
		TestPopulatedDiffsSignalsAndTraceConformToTheContract(t)
		TestContextPullsConformToTheContract(t)
		TestHealthAndIngestConformToTheContract(t)
		TestStrategyEndpointsConformToTheContract(t)
		TestProviderBreakerEndpointsConformToTheContract(t)
		TestRunStatusAndOutboxConformToTheContract(t)
		TestSurfaceMessageRefsConformToTheContract(t)
		if neo4jtest.Available() {
			TestGraphEndpointsConformToTheContract(t)
		}
	})
	checked := checkedOperations
	var missing []string
	for _, op := range s.spec.Operations() {
		key := op.Method + " " + op.Path
		if _, isPending := pending[key]; isPending {
			continue
		}
		if graphOperations[key] && !neo4jtest.Available() {
			continue // exercised by graph_test.go wherever Neo4j can run (CI always; locally with Java 17/21)
		}
		if !checked[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("operations in core.yaml with no conformance test and not listed as pending: %v", missing)
	}
	for key := range pending {
		method, path, _ := strings.Cut(key, " ")
		concrete := strings.NewReplacer("{account_id}", s.world.AccountA, "{run_id}", s.world.RunA, "{claim_id}", missingID, "{episode_id}", missingID).Replace(path)
		r := s.do(method, concrete, "", apiToken, nil)
		if r.status != http.StatusNotFound && r.status != http.StatusMethodNotAllowed {
			t.Errorf("%s is listed as pending but answered %d; move it to a real conformance test", key, r.status)
		}
	}
}
