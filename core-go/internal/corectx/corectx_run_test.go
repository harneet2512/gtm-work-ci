package corectx_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
)

func TestOnlyUnfinishedRunsMayPull(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	run := ctxfixture.FreshRun(t, env.DB, w.AccountA, "pending")
	for status, want := range map[string]bool{
		"pending": true, "context_built": true, "drafted": true,
		"awaiting_human": false, "approved": false, "edited": false, "rejected": false,
		"ignored": false, "executed": false, "failed": false, "cancelled": false,
	} {
		if _, err := env.DB.Exec(`UPDATE agent_runs SET status = $2, run_mode = 'live' WHERE id = $1::uuid`, run, status); err != nil {
			t.Fatalf("set %s: %v", status, err)
		}
		_, err := service(t).Pull(context.Background(), run, corectx.ToolPeople, corectx.Params{})
		if want && err != nil {
			t.Errorf("status %s: pull refused: %v", status, err)
		}
		if !want && !errors.Is(err, corectx.ErrRunInactive) {
			t.Errorf("status %s: pull = %v, want ErrRunInactive", status, err)
		}
	}
}

func TestAnUnknownRunCannotPull(t *testing.T) {
	ctxfixture.Get(t, env.DB)
	_, err := service(t).Pull(context.Background(), "99999999-9999-4999-8999-999999999999", corectx.ToolState, corectx.Params{})
	if !errors.Is(err, corectx.ErrRunInactive) {
		t.Fatalf("unknown run: %v", err)
	}
}

func TestRefusedPullsAreNotLogged(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	run := ctxfixture.FreshRun(t, env.DB, w.AccountA, "awaiting_human")
	_, _ = service(t).Pull(context.Background(), run, corectx.ToolState, corectx.Params{})
	_, _ = service(t).Pull(context.Background(), run, "no_such_tool", corectx.Params{})
	if n := scalar(t, `SELECT count(*)::text FROM context_access_log WHERE agent_run_id = $1::uuid`, run); n != "0" {
		t.Fatalf("%s log rows for refused pulls", n)
	}
}

func TestEveryServedPullIsLoggedWithTheCitedAccessID(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	run := ctxfixture.FreshRun(t, env.DB, w.AccountA, "context_built")
	p1 := pull(t, run, corectx.ToolState, corectx.Params{FieldPath: "stage", Limit: 3})
	p2 := pull(t, run, corectx.ToolActivities, corectx.Params{Limit: 2})
	if p2.AccessID <= p1.AccessID {
		t.Fatalf("access ids %d then %d are not increasing", p1.AccessID, p2.AccessID)
	}
	rows, err := env.DB.Query(`SELECT id, tool, args::text, returned_ids::text, bytes, truncated FROM context_access_log
 WHERE agent_run_id = $1::uuid ORDER BY id`, run)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id int64
		var tool, args, ids string
		var bytes int
		var truncated bool
		if err := rows.Scan(&id, &tool, &args, &ids, &bytes, &truncated); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%d|%s|%s|%d|%v", id, tool, args, bytes, truncated))
	}
	if p1.WorldAsOf == nil || p2.WorldAsOf == nil {
		t.Fatal("a run always has a trigger activity, so every packet carries its world cutoff (ADR-0019)")
	}
	cutoff := func(p corectx.Packet) string { return p.WorldAsOf.UTC().Format(time.RFC3339Nano) }
	want := []string{
		fmt.Sprintf("%d|state|{\"limit\": 3, \"field_path\": \"stage\", \"world_as_of\": %q}|%d|%v", p1.AccessID, cutoff(p1), p1.Bytes, p1.Truncated),
		fmt.Sprintf("%d|activities|{\"limit\": 2, \"world_as_of\": %q}|%d|%v", p2.AccessID, cutoff(p2), p2.Bytes, p2.Truncated),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("log =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLimitBoundsItemsAndSetsTruncated(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	p := pull(t, fresh(t, w.AccountA), corectx.ToolActivities, corectx.Params{Limit: 1})
	if len(p.Items) != 1 || !p.Truncated {
		t.Fatalf("limit 1: %d items, truncated=%v (the account has more activities)", len(p.Items), p.Truncated)
	}
	if def := pull(t, fresh(t, w.AccountA), corectx.ToolActivities, corectx.Params{}); len(def.Items) > corectx.DefaultLimit || def.Limits.MaxItems != corectx.DefaultLimit {
		t.Fatalf("default limit: %d items, limits %+v", len(def.Items), def.Limits)
	}
}

func TestPacketsStayWithinTheByteBudgetEvenForHugeActivities(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	run := ctxfixture.FreshRun(t, env.DB, w.AccountB, "context_built")
	for i := 0; i < 25; i++ {
		obj := fmt.Sprintf("huge-%d", i)
		if _, err := env.DB.Exec(`WITH ev AS (
 INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
 VALUES ('email', $1::text, 'received', encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), '{}'::jsonb) RETURNING id)
 INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance, summary, body_text)
 SELECT id, 'EmailReceived', 'email', $1, now() + ($2 || ' seconds')::interval, $3::uuid,
        '{"source_system":"email","source_object_id":"x"}'::jsonb, repeat('s', 900), repeat('b', 20000) FROM ev`,
			obj, fmt.Sprint(i), w.AccountB); err != nil {
			t.Fatal(err)
		}
	}
	p := pull(t, run, corectx.ToolActivities, corectx.Params{Limit: corectx.MaxLimit})
	raw, _ := json.Marshal(p.Items)
	if p.Bytes > corectx.MaxPacketBytes || len(raw) != p.Bytes {
		t.Fatalf("bytes = %d (serialized %d), budget %d", p.Bytes, len(raw), corectx.MaxPacketBytes)
	}
	if !p.Truncated || len(p.Items) == 0 || len(p.Items) >= corectx.MaxLimit {
		t.Fatalf("a packet over budget must be cut and flagged: %d items, truncated=%v", len(p.Items), p.Truncated)
	}
	clippedSeen := false
	for _, item := range p.Items {
		if excerpt, ok := decode(t, item)["excerpt"].(string); ok {
			clippedSeen = true
			if n := len(excerpt); n > 520 {
				t.Fatalf("excerpt of %d bytes survived per-item clipping", n)
			}
		}
	}
	if !clippedSeen {
		t.Fatal("no huge activity reached the packet; the test proved nothing")
	}
}

func TestParametersAreValidated(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	svc := service(t)
	bad := []struct {
		tool corectx.Tool
		p    corectx.Params
	}{
		{corectx.ToolState, corectx.Params{Limit: -1}},
		{corectx.ToolState, corectx.Params{Limit: corectx.MaxLimit + 1}},
		{corectx.ToolState, corectx.Params{FieldPath: "'; DROP TABLE claims; --"}},
		{corectx.ToolState, corectx.Params{FieldPath: "account_id"}},
		{corectx.ToolEvidence, corectx.Params{}},
	}
	for _, c := range bad {
		if _, err := svc.Pull(context.Background(), fresh(t, w.AccountA), c.tool, c.p); !errors.Is(err, corectx.ErrBadParams) {
			t.Errorf("%s %+v: %v, want ErrBadParams", c.tool, c.p, err)
		}
	}
	if _, err := svc.Pull(context.Background(), fresh(t, w.AccountA), "../etc/passwd", corectx.Params{}); !errors.Is(err, corectx.ErrUnknownTool) {
		t.Errorf("unknown tool: %v", err)
	}
}

func TestAnAccountWithoutStateGivesEmptyPacketsNotErrors(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	var account string
	if err := env.DB.QueryRow(`INSERT INTO accounts (name) VALUES ('No state yet') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	var activity, eval, run string
	if err := env.DB.QueryRow(`WITH ev AS (
 INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
 VALUES ('email', 'nostate', 'received', encode(sha256(convert_to('nostate', 'UTF8')), 'hex'), '{}'::jsonb) RETURNING id)
 INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
 SELECT id, 'EmailReceived', 'email', 'nostate', now(), $1::uuid, '{"source_system":"email","source_object_id":"x"}'::jsonb FROM ev RETURNING id::text`, account).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.QueryRow(`INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes)
 VALUES ($1::uuid, 'post_interaction_followup', true, ARRAY['eligible_customer_replied']) RETURNING id::text`, account).Scan(&eval); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.QueryRow(`INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids)
 VALUES ($1::uuid, 'post_interaction_followup', 'dry_run', 'pending', $2::uuid, ARRAY[$3::uuid]) RETURNING id::text`, account, eval, activity).Scan(&run); err != nil {
		t.Fatal(err)
	}
	_ = w
	for _, tool := range []corectx.Tool{corectx.ToolState, corectx.ToolPeople, corectx.ToolCommitments, corectx.ToolRecentDiffs} {
		p := pull(t, run, tool, corectx.Params{})
		if p.Items == nil || len(p.Items) != 0 || p.Truncated {
			t.Errorf("%s: items=%v truncated=%v, want an empty untruncated packet", tool, p.Items, p.Truncated)
		}
	}
	if p := pull(t, run, corectx.ToolActivities, corectx.Params{}); len(p.Items) != 1 {
		t.Errorf("activities = %d, want the one trigger", len(p.Items))
	}
}

func TestARunHasAPullBudget(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	run := ctxfixture.FreshRun(t, env.DB, w.AccountA, "context_built")
	if _, err := env.DB.Exec(`INSERT INTO context_access_log (agent_run_id, tool, bytes)
 SELECT $1::uuid, 'state', 2 FROM generate_series(1, $2)`, run, corectx.MaxPulls-1); err != nil {
		t.Fatal(err)
	}
	pull(t, run, corectx.ToolState, corectx.Params{Limit: 1}) // the last allowed pull
	if _, err := service(t).Pull(context.Background(), run, corectx.ToolState, corectx.Params{}); !errors.Is(err, corectx.ErrTooManyPulls) {
		t.Fatalf("pull %d: %v, want ErrTooManyPulls", corectx.MaxPulls+1, err)
	}
}

// A field with more claims than the scan bound is cut at the bound and the packet says so; the cut is never silent.
func TestEvidenceScanBoundIsReportedAsTruncation(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	field := scalar(t, `SELECT field_path FROM claims WHERE account_id = $1::uuid AND field_path <> 'amount' GROUP BY field_path ORDER BY count(*) DESC, field_path LIMIT 1`, w.AccountA)
	all := pull(t, fresh(t, w.AccountA), corectx.ToolEvidence, corectx.Params{FieldPath: field, Limit: 20})
	if len(all.Items) < 2 {
		t.Fatalf("setup: %s has %d visible claims, the test needs at least 2", field, len(all.Items))
	}
	if all.Truncated {
		t.Fatal("setup: the unbounded read must not be truncated")
	}
	defer corectx.SetMaxEvidenceScan(len(all.Items) - 1)()
	cut := pull(t, fresh(t, w.AccountA), corectx.ToolEvidence, corectx.Params{FieldPath: field, Limit: 20})
	if !cut.Truncated || len(cut.Items) != len(all.Items)-1 {
		t.Fatalf("bound %d: %d items, truncated=%v; want %d items and truncated", len(all.Items)-1, len(cut.Items), cut.Truncated, len(all.Items)-1)
	}
}
