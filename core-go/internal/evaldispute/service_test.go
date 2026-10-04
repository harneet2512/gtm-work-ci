package evaldispute_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/evaldispute"
	"github.com/harneet2512/gtm-work/core-go/internal/evaldispute/disputetest"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

const unknownID = "99999999-9999-4999-8999-999999999999"

type fixture struct {
	t    *testing.T
	ctx  context.Context
	svc  *evaldispute.Service
	seed strategytest.Seeded
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	world := ctxfixture.Get(t, env.DB)
	svc, err := evaldispute.New(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, ctx: context.Background(), svc: svc, seed: strategytest.Seed(t, env.DB, world.AccountA)}
}

func (f *fixture) result(evalType, verdict string, blocking bool) string {
	return disputetest.SeedResult(f.t, env.DB, disputetest.Result{RunID: f.seed.RunID, DraftIndex: 1, EvalType: evalType, Verdict: verdict, Blocking: blocking})
}

func ptr(s string) *string { return &s }

func request(reason string, expected *string) evaldispute.Request {
	return evaldispute.Request{Reason: reason, ExpectedVerdict: expected, Surface: "web", ActorLabel: "Dana Kim"}
}

func decode(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	valid(t, doc)
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func valid(t *testing.T, doc []byte) {
	t.Helper()
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("eval_dispute", doc); err != nil {
		t.Fatalf("not a valid EvalDispute: %v\n%s", err, doc)
	}
}

func refusedCode(t *testing.T, err error) string {
	t.Helper()
	var r *evaldispute.RefusedError
	if !errors.As(err, &r) {
		t.Fatalf("want a RefusedError, got %v", err)
	}
	return r.Code
}

func TestDisputeSnapshotsTheResultItDisputes(t *testing.T) {
	f := newFixture(t)
	id := f.result("champion_continuity", "fail", true)

	doc, created, err := f.svc.Dispute(f.ctx, id, request("  Marco owns the security review; this should warn, not block.  ", ptr("warn")))
	if err != nil || !created {
		t.Fatalf("dispute: created=%v err=%v", created, err)
	}
	m := decode(t, doc)
	want := map[string]any{
		"eval_result_id": id, "agent_run_id": f.seed.RunID, "draft_index": float64(1), "eval_type": "champion_continuity",
		"eval_version": "champion_continuity:v1", "disputed_verdict": "fail", "disputed_blocking": true,
		"expected_verdict": "warn", "reason": "Marco owns the security review; this should warn, not block.",
		"surface": "web", "actor_label": "Dana Kim", "actor_person_id": nil,
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
}

func TestAnIdenticalDisputeConvergesOnOneRow(t *testing.T) {
	f := newFixture(t)
	id := f.result("cta_calibration", "pass", false)
	req := request("Asks for a signature before security review is closed.", ptr("fail"))

	first, created, err := f.svc.Dispute(f.ctx, id, req)
	if err != nil || !created {
		t.Fatalf("first: %v %v", created, err)
	}
	again, created, err := f.svc.Dispute(f.ctx, id, req)
	if err != nil || created {
		t.Fatalf("repeat must return the stored dispute: created=%v err=%v", created, err)
	}
	if decode(t, first)["id"] != decode(t, again)["id"] {
		t.Fatalf("repeat made a second dispute:\n%s\n%s", first, again)
	}
	other, created, err := f.svc.Dispute(f.ctx, id, request("A different reason.", ptr("fail")))
	if err != nil || !created || decode(t, other)["id"] == decode(t, first)["id"] {
		t.Fatalf("a different reason is a new dispute: created=%v err=%v", created, err)
	}
	var n int
	if err := env.DB.QueryRow(`SELECT count(*) FROM eval_disputes WHERE eval_result_id = $1::uuid`, id).Scan(&n); err != nil || n != 2 {
		t.Fatalf("want 2 disputes, got %d (%v)", n, err)
	}
}

func TestAReasoningOnlyDisputeHasNoExpectedVerdict(t *testing.T) {
	f := newFixture(t)
	id := f.result("grounding", "pass", false)
	doc, _, err := f.svc.Dispute(f.ctx, id, request("Right verdict, but the quote is from the wrong call.", nil))
	if err != nil {
		t.Fatal(err)
	}
	if m := decode(t, doc); m["expected_verdict"] != nil {
		t.Fatalf("expected_verdict = %v, want null", m["expected_verdict"])
	}
}

func TestAnExpectedVerdictMustDiffer(t *testing.T) {
	f := newFixture(t)
	id := f.result("cta_calibration", "warn", false)
	_, _, err := f.svc.Dispute(f.ctx, id, request("Should warn.", ptr("warn")))
	if code := refusedCode(t, err); code != evaldispute.CodeExpectedEqualsVerdict {
		t.Fatalf("code %s", code)
	}
}

func TestUnknownResultsAreNotFound(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{unknownID, "not-a-uuid", ""} {
		if _, _, err := f.svc.Dispute(f.ctx, id, request("x", nil)); !errors.Is(err, evaldispute.ErrNotFound) {
			t.Fatalf("%q: want ErrNotFound, got %v", id, err)
		}
	}
}

func TestInvalidRequestsAreRefused(t *testing.T) {
	f := newFixture(t)
	id := f.result("grounding", "pass", false)
	cases := map[string]func(*evaldispute.Request){
		"empty reason":      func(r *evaldispute.Request) { r.Reason = "" },
		"blank reason":      func(r *evaldispute.Request) { r.Reason = " \n\t " },
		"reason too long":   func(r *evaldispute.Request) { r.Reason = strings.Repeat("é", 2001) },
		"unknown surface":   func(r *evaldispute.Request) { r.Surface = "email" },
		"empty actor label": func(r *evaldispute.Request) { r.ActorLabel = "" },
		"long actor label":  func(r *evaldispute.Request) { r.ActorLabel = strings.Repeat("a", 201) },
		"bad expected":      func(r *evaldispute.Request) { r.ExpectedVerdict = ptr("maybe") },
		"malformed person":  func(r *evaldispute.Request) { r.ActorPersonID = ptr("dana") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := request("The eval missed the reorg.", nil)
			mutate(&req)
			_, _, err := f.svc.Dispute(f.ctx, id, req)
			if code := refusedCode(t, err); code != evaldispute.CodeInvalidRequest {
				t.Fatalf("code %s", code)
			}
		})
	}
}

func TestTheLongestReasonIsCountedInCharacters(t *testing.T) {
	var encoding string
	if err := env.DB.QueryRow(`SHOW server_encoding`).Scan(&encoding); err != nil {
		t.Fatal(err)
	}
	if encoding != "UTF8" {
		// The Windows embedded server runs client and server encoding WIN1252, so each UTF-8 "é" arrives as two
		// characters; CI (Linux) and Neon run UTF8, where this check is exact.
		t.Skipf("server_encoding is %s, not UTF8: multi-byte characters are mis-counted here", encoding)
	}
	f := newFixture(t)
	id := f.result("grounding", "pass", false)
	if _, _, err := f.svc.Dispute(f.ctx, id, request(strings.Repeat("é", 2000), nil)); err != nil {
		t.Fatalf("2000 two-byte characters are within the limit: %v", err)
	}
}

func TestAnUnknownActorPersonIsRefused(t *testing.T) {
	f := newFixture(t)
	id := f.result("grounding", "pass", false)
	req := request("x", nil)
	req.ActorPersonID = ptr(unknownID)
	if code := refusedCode(t, func() error { _, _, err := f.svc.Dispute(f.ctx, id, req); return err }()); code != evaldispute.CodeUnknownPerson {
		t.Fatalf("code %s", code)
	}
	req.ActorPersonID = ptr(f.seed.Marco)
	doc, _, err := f.svc.Dispute(f.ctx, id, req)
	if err != nil || decode(t, doc)["actor_person_id"] != f.seed.Marco {
		t.Fatalf("a known person is recorded: %v", err)
	}
}

func TestDisputesAreAppendOnly(t *testing.T) {
	f := newFixture(t)
	id := f.result("grounding", "pass", false)
	if _, _, err := f.svc.Dispute(f.ctx, id, request("x", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`UPDATE eval_disputes SET reason = 'rewritten' WHERE eval_result_id = $1::uuid`, id); err == nil {
		t.Fatal("a dispute was rewritten")
	}
}

func TestADisputeNeverChangesTheResult(t *testing.T) {
	f := newFixture(t)
	id := f.result("champion_continuity", "fail", true)
	if _, _, err := f.svc.Dispute(f.ctx, id, request("Should not block.", ptr("pass"))); err != nil {
		t.Fatal(err)
	}
	var verdict string
	var blocking bool
	if err := env.DB.QueryRow(`SELECT verdict, blocking FROM eval_runs WHERE id = $1::uuid`, id).Scan(&verdict, &blocking); err != nil {
		t.Fatal(err)
	}
	if verdict != "fail" || !blocking {
		t.Fatalf("the disputed result changed: %s blocking=%v", verdict, blocking)
	}
}

func TestNewRefusesANilDatabase(t *testing.T) {
	if _, err := evaldispute.New(nil, nil); err == nil {
		t.Fatal("New(nil) must fail")
	}
}
